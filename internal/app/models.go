package app

import (
	"errors"
	"fmt"
	"time"

	"tala/internal/models"
)

type modelTransfer struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
}

type modelInventory struct {
	Statuses map[string]models.Status `json:"statuses"`
	Busy     bool                     `json:"busy"`
	Download modelTransfer            `json:"download"`
}

func (c *Controller) modelInventoryLocked() modelInventory {
	return modelInventory{Statuses: c.modelStates, Busy: !c.modelScanDone || c.modelBusy, Download: c.modelDownload}
}

func (c *Controller) emitModelsLocked() {
	c.emitLocked("modelInventory", c.modelInventoryLocked())
}

func (c *Controller) scanModels(settings models.Settings) map[string]models.Status {
	statuses := make(map[string]models.Status)
	for _, model := range models.Catalog() {
		status, _ := c.Models.DiscoverModel(c.ctx, settings, model.ID)
		statuses[model.ID] = status
	}
	if !c.headless && cleanupExecutable(c.Models.Exe) != "" {
		statuses[models.CleanupID], _ = c.Models.DiscoverModel(c.ctx, settings, models.CleanupID)
	}
	return statuses
}

func (c *Controller) updateModelStatesLocked(settings models.Settings, statuses map[string]models.Status) {
	c.modelStates = statuses
	c.Model = statuses[c.Models.EffectiveID(settings, statuses)]
	c.modelScanDone = true
	c.emitLocked("model", c.Model)
}

func (c *Controller) finishModelOperation() {
	c.mu.Lock()
	c.modelBusy = false
	c.modelDownload = modelTransfer{}
	c.emitModelsLocked()
	c.mu.Unlock()
	c.signal()
}

func (c *Controller) Discover() {
	c.mu.Lock()
	if c.modelBusy {
		c.mu.Unlock()
		return
	}
	c.modelBusy = true
	c.emitModelsLocked()
	c.mu.Unlock()
	defer c.finishModelOperation()
	settings := c.recognitionSettings()
	statuses := c.scanModels(settings)
	c.mu.Lock()
	c.updateModelStatesLocked(c.recognitionSettings(), statuses)
	c.mu.Unlock()
}

func (c *Controller) modelForRequestLocked(id string, requireInstalled bool) (models.Status, error) {
	if id == "" {
		id = c.Model.ID
	}
	if id == "" {
		id = models.DefaultID
	}
	spec, ok := models.Lookup(id)
	if !ok {
		return models.Status{}, fmt.Errorf("unknown transcription model %q", id)
	}
	status := c.modelStates[id]
	status.ID = id
	if requireInstalled && status.Path == "" {
		return status, fmt.Errorf("install %s in Settings before transcribing", spec.Name)
	}
	return status, nil
}

func (c *Controller) jobModelLocked(job Job) models.Status {
	id := job.ModelID
	if id == "" {
		id = models.FullTurboID // Older queued jobs predate model selection.
	}
	status := c.modelStates[id]
	status.ID = id
	return status
}

func (c *Controller) Configure(s models.Settings) error {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	if err := s.URLAuth.Validate(); err != nil {
		return err
	}
	if !models.ValidLanguage(s.Language) {
		return errors.New("choose Taglish, automatic, English, or Filipino")
	}
	c.mu.Lock()
	if c.live.Active || c.activeJob != "" || c.modelBusy {
		c.mu.Unlock()
		return errors.New("wait for active work before changing settings")
	}
	c.modelBusy = true
	c.mu.Unlock()
	defer c.finishModelOperation()
	old := c.Models.Settings()
	// Model preferences have their own controls, so a recognition save cannot overwrite them.
	s.DefaultModel, s.AskModel = old.DefaultModel, old.AskModel
	s.LiveReview, s.LiveReviewModel = old.LiveReview, old.LiveReviewModel
	pathsChanged := old.ModelPath != s.ModelPath || old.ModelDir != s.ModelDir
	if s.ModelPath != old.ModelPath && s.ModelPath != "" {
		model, err := c.Models.IdentifyPath(c.ctx, s.ModelPath)
		if err != nil {
			return err
		}
		s.DefaultModel = model.ID
	}
	if err := c.Models.Save(s); err != nil {
		return err
	}
	if pathsChanged {
		statuses := c.scanModels(s)
		c.mu.Lock()
		c.updateModelStatesLocked(s, statuses)
		c.mu.Unlock()
	}
	c.Emit("settings", s)
	return nil
}

func (c *Controller) SetModelPreference(id string, ask bool) error {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if id != "" {
		if _, err := c.modelForRequestLocked(id, true); err != nil {
			return err
		}
	}
	s := c.Models.Settings()
	if id != "" {
		s.DefaultModel = id
	}
	s.AskModel = ask
	if err := c.Models.Save(s); err != nil {
		return err
	}
	if c.modelScanDone {
		c.updateModelStatesLocked(s, c.modelStates)
	}
	c.emitLocked("settings", s)
	return nil
}

func (c *Controller) DownloadModel(id string) error {
	spec, ok := models.LookupReview(id)
	if !ok {
		return fmt.Errorf("unknown model %q", id)
	}
	if id == models.CleanupID && (c.headless || cleanupExecutable(c.Models.Exe) == "") {
		return errors.New("contextual cleanup requires the bundled Tala app")
	}
	c.mu.Lock()
	if !c.modelScanDone || c.modelBusy {
		c.mu.Unlock()
		return errors.New("another model operation is in progress")
	}
	c.modelBusy = true
	c.modelDownload.ID = id
	c.emitModelsLocked()
	c.mu.Unlock()
	defer c.finishModelOperation()
	var lastUpdate time.Time
	status, err := c.Models.Download(c.ctx, c.Models.Settings(), id, func(n int64) {
		if n == spec.Bytes || time.Since(lastUpdate) >= 150*time.Millisecond {
			lastUpdate = time.Now()
			c.mu.Lock()
			c.modelDownload.Bytes = n
			c.emitModelsLocked()
			c.mu.Unlock()
		}
	})
	if err != nil {
		return err
	}
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	s := c.Models.Settings()
	c.mu.Lock()
	defer c.mu.Unlock()
	statuses := make(map[string]models.Status)
	hasModel := false
	for key, current := range c.modelStates {
		statuses[key] = current
		hasModel = hasModel || (key != models.CleanupID && current.Path != "")
	}
	statuses[id] = status
	if id != models.CleanupID && !hasModel && s.DefaultModel == "" {
		s.DefaultModel = id
		if err := c.Models.Save(s); err != nil {
			c.updateModelStatesLocked(c.Models.Settings(), statuses)
			return err
		}
		c.emitLocked("settings", s)
	}
	c.updateModelStatesLocked(s, statuses)
	return nil
}

func (c *Controller) RemoveModel(id string) error {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	c.mu.Lock()
	if c.modelBusy || c.live.Active || c.activeJob != "" {
		c.mu.Unlock()
		return errors.New("wait for active work before removing a model")
	}
	for _, job := range c.jobs {
		if job.State == "waiting" && c.jobModelLocked(job).ID == id {
			c.mu.Unlock()
			return errors.New("this model is needed by an import in the queue")
		}
	}
	status := c.modelStates[id]
	if !status.Managed {
		c.mu.Unlock()
		return errors.New("choose a model downloaded by Tala")
	}
	c.modelBusy = true
	c.emitModelsLocked()
	c.mu.Unlock()
	defer c.finishModelOperation()

	c.engineMu.Lock()
	if c.enginePath == status.Path {
		c.closeEngine()
	}
	c.engineMu.Unlock()
	if err := c.Models.Remove(status); err != nil {
		return err
	}
	s := c.Models.Settings()
	statuses := c.scanModels(s)
	c.mu.Lock()
	c.updateModelStatesLocked(s, statuses)
	c.mu.Unlock()
	return nil
}

func (c *Controller) ModelStateFor(id string) models.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.modelScanDone || c.modelBusy && c.modelDownload.ID == "" {
		return models.Status{ID: id, State: "checking"}
	}
	if id == "" {
		return c.Model
	}
	return c.modelStates[id]
}
