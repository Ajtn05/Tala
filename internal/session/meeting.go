package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// Meeting keeps user notes separate from generated notes and the source transcript.
type Meeting struct {
	Revision           uint64    `json:"revision"`
	Notes              string    `json:"notes"`
	Summary            string    `json:"summary"`
	SummaryModel       string    `json:"summaryModel,omitempty"`
	SummaryUpdated     time.Time `json:"summaryUpdated,omitempty"`
	SummaryFingerprint string    `json:"summaryFingerprint,omitempty"`
	SummaryStale       bool      `json:"summaryStale,omitempty"`
}

// MeetingFingerprint identifies the evidence used for a summary, including corrections.
func MeetingFingerprint(v Session) string {
	type line struct {
		Text, Source string
		Start, End   float64
	}
	evidence := struct {
		Notes string
		Lines []line
	}{}
	if v.Meeting != nil {
		evidence.Notes = v.Meeting.Notes
	}
	for _, seg := range v.Segments {
		evidence.Lines = append(evidence.Lines, line{Text(v, seg), seg.Source, seg.Start, seg.End})
	}
	b, _ := json.Marshal(evidence)
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}

func markMeetingStale(v *Session) {
	if v.Meeting != nil {
		v.Meeting.SummaryStale = v.Meeting.Summary != "" && v.Meeting.SummaryFingerprint != MeetingFingerprint(*v)
	}
}

func (s *Store) SaveMeetingNotes(id, notes string) (*Meeting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.read(id, true)
	if err != nil {
		return nil, err
	}
	if v.Meeting == nil {
		v.Meeting = &Meeting{}
	}
	v.Meeting.Notes = notes
	v.Meeting.Revision++
	markMeetingStale(&v)
	return v.Meeting, s.save(v)
}

// SaveMeetingSummary merges only generated fields so concurrent note edits survive.
func (s *Store) SaveMeetingSummary(id, summary, modelID, fingerprint string) (*Meeting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.read(id, true)
	if err != nil {
		return nil, err
	}
	if v.Meeting == nil {
		v.Meeting = &Meeting{}
	}
	v.Meeting.Summary = summary
	v.Meeting.Revision++
	v.Meeting.SummaryModel = modelID
	v.Meeting.SummaryUpdated = time.Now()
	v.Meeting.SummaryFingerprint = fingerprint
	markMeetingStale(&v)
	return v.Meeting, s.save(v)
}

// SaveEnhancedNotes edits the generated version without changing the user's
// source notes or claiming that new model inference took place.
func (s *Store) SaveEnhancedNotes(id, text string) (*Meeting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.read(id, true)
	if err != nil {
		return nil, err
	}
	if v.Meeting == nil || v.Meeting.SummaryFingerprint == "" {
		return nil, errors.New("enhance your notes before editing the enhanced version")
	}
	v.Meeting.Summary = text
	v.Meeting.Revision++
	markMeetingStale(&v)
	return v.Meeting, s.save(v)
}
