package app

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

type meetingTarget struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Account  string `json:"account,omitempty"`
}

type meetingProvider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DefaultModel string `json:"defaultModel"`
	HasKey       bool   `json:"hasKey"`
}

var meetingProviders = []meetingProvider{
	{ID: chatGPTMeetingProvider, Name: "ChatGPT plan"},
	{ID: "gemini", Name: "Google Gemini", DefaultModel: "gemini-3.8-flash"},
	{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-5.4-mini"},
	{ID: "anthropic", Name: "Anthropic Claude", DefaultModel: "claude-haiku-5-5"},
}

var meetingModelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)

func lookupMeetingProvider(id string) (meetingProvider, bool) {
	for _, provider := range meetingProviders {
		if provider.ID == id {
			return provider, true
		}
	}
	return meetingProvider{}, false
}

type meetingKeyStore interface {
	Has(provider string) bool
	Load(provider string) (string, error)
	Save(provider, key string) error
	Remove(provider string) error
}

type meetingProviderState struct {
	meetingTarget
	Providers       []meetingProvider `json:"providers"`
	ChatGPTAccounts []chatGPTAccount  `json:"chatGPTAccounts,omitempty"`
}

func (c *Controller) loadMeetingProviders() {
	c.meetingKeys = newMeetingKeyStore()
	if c.meetingKeys == nil {
		return
	}
	c.meetingKeyReady = make(map[string]bool, len(meetingProviders))
	for _, provider := range meetingProviders {
		if provider.ID != chatGPTMeetingProvider {
			c.meetingKeyReady[provider.ID] = c.meetingKeys.Has(provider.ID)
		}
	}
	c.chatGPT = loadChatGPTPlan(c.meetingKeys, c.Models.Root())
	c.chatGPTAccounts = c.chatGPT.accounts()
	c.chatGPTLimited = make(map[string]bool)
	s := c.Models.Settings()
	if provider, ok := lookupMeetingProvider(s.MeetingProvider); ok {
		model := s.MeetingModel
		if !meetingModelID.MatchString(model) {
			model = provider.DefaultModel
		}
		c.meetingTarget = meetingTarget{Provider: provider.ID, Model: model}
		if provider.ID == chatGPTMeetingProvider {
			c.meetingTarget.Account = s.MeetingAccount
		}
	}
}

func (c *Controller) meetingProvidersLocked() meetingProviderState {
	state := meetingProviderState{meetingTarget: c.meetingTarget, ChatGPTAccounts: c.chatGPTAccounts}
	if c.meetingKeys != nil {
		state.Providers = append([]meetingProvider(nil), meetingProviders...)
		for i := range state.Providers {
			state.Providers[i].HasKey = c.meetingKeyReady[state.Providers[i].ID]
		}
	}
	return state
}

func (c *Controller) ConfigureMeetingProvider(providerID, model, key, account string) (any, error) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	c.mu.Lock()
	busy := c.summaryID != "" || c.chatGPTDone != nil
	var chatGPT chatGPTAccount
	if providerID == chatGPTMeetingProvider {
		chatGPT = c.chatGPTAccountLocked(account)
	} else {
		account = ""
	}
	c.mu.Unlock()
	if busy {
		return nil, errors.New("finish or cancel enhancement before changing its provider")
	}
	model, key = strings.TrimSpace(model), strings.TrimSpace(key)
	if providerID == "" {
		model = ""
	} else if providerID == chatGPTMeetingProvider {
		if !chatGPT.Authorized {
			return nil, errors.New("connect an account and allow ChatGPT plan usage first")
		}
		if !chatGPT.hasModel(model) {
			return nil, errors.New("choose a model available to this ChatGPT account")
		}
	} else {
		provider, ok := lookupMeetingProvider(providerID)
		if !ok || c.meetingKeys == nil {
			return nil, errors.New("choose an available meeting notes provider")
		}
		if model == "" {
			model = provider.DefaultModel
		}
		if !meetingModelID.MatchString(model) {
			return nil, errors.New("enter the model ID from your provider’s API")
		}
		if key == "" {
			if !c.meetingKeys.Has(providerID) {
				return nil, errors.New("enter an API key for " + provider.Name)
			}
		} else {
			if len(key) > 2048 || strings.ContainsFunc(key, func(r rune) bool { return r < 33 || r > 126 }) {
				return nil, errors.New("enter a valid provider API key")
			}
			if err := c.meetingKeys.Save(providerID, key); err != nil {
				return nil, err
			}
		}
		c.mu.Lock()
		c.meetingKeyReady[providerID] = true
		c.mu.Unlock()
	}
	s := c.Models.Settings()
	s.MeetingProvider, s.MeetingModel, s.MeetingAccount = providerID, model, account
	if err := c.Models.Save(s); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.meetingTarget = meetingTarget{Provider: providerID, Model: model, Account: account}
	c.emitMeetingSummaryLocked()
	return c.meetingProvidersLocked(), nil
}

func (c *Controller) RemoveMeetingProviderKey(providerID string) (any, error) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	if _, ok := lookupMeetingProvider(providerID); !ok || providerID == chatGPTMeetingProvider || c.meetingKeys == nil {
		return nil, errors.New("choose a provider with a saved API key")
	}
	c.mu.Lock()
	busy := c.summaryID != ""
	c.mu.Unlock()
	if busy {
		return nil, errors.New("finish or cancel enhancement before removing an API key")
	}
	if err := c.meetingKeys.Remove(providerID); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.meetingKeyReady[providerID] = false
	c.emitMeetingSummaryLocked()
	return c.meetingProvidersLocked(), nil
}

func (c *Controller) chatGPTAccountLocked(id string) chatGPTAccount {
	for _, account := range c.chatGPTAccounts {
		if account.ID == id {
			return account
		}
	}
	return chatGPTAccount{}
}

func (a chatGPTAccount) hasModel(id string) bool {
	return slices.ContainsFunc(a.Models, func(model chatGPTModel) bool { return model.ID == id })
}

type chatGPTSettingsResult struct {
	meetingProviderState
	Selected string `json:"selected"`
	Welcome  bool   `json:"welcome,omitempty"`
	Notice   string `json:"notice,omitempty"`
}

func (c *Controller) ChangeChatGPTAccount(operation, account string, openBrowser func(string) error) (any, error) {
	c.settingsMu.Lock()
	c.mu.Lock()
	c.settingsMu.Unlock()
	if c.chatGPT == nil || c.ctx.Err() != nil || c.summaryID != "" || c.chatGPTDone != nil {
		c.mu.Unlock()
		return nil, errors.New("finish active enhancement or account changes first")
	}
	timeout := 25 * time.Second
	if operation == "signIn" {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	done := make(chan struct{})
	c.chatGPTDone, c.chatGPTCancel = done, cancel
	c.emitMeetingSummaryLocked()
	c.mu.Unlock()
	defer cancel()

	var result chatGPTSettingsResult
	var err error
	result.Selected = account
	switch operation {
	case "signIn":
		result.Selected, result.Welcome, result.Notice, err = c.chatGPT.signIn(ctx, account, openBrowser)
	case "signOut":
		result.Notice, err = c.chatGPT.signOut(ctx, account)
	case "refresh":
		result.Welcome, err = c.chatGPT.refreshModels(ctx, account)
	default:
		err = errors.New("choose a ChatGPT account action")
	}
	accounts := c.chatGPT.accounts()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chatGPTAccounts = accounts
	c.chatGPTDone, c.chatGPTCancel = nil, nil
	if err == nil {
		delete(c.chatGPTLimited, result.Selected)
	}
	c.emitMeetingSummaryLocked()
	result.meetingProviderState = c.meetingProvidersLocked()
	c.emitLocked("meetingProviders", result.meetingProviderState)
	close(done)
	return result, err
}

func (c *Controller) CancelChatGPTConnection() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.chatGPTCancel != nil {
		c.chatGPTCancel()
	}
}
