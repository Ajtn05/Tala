// Package session stores metadata atomically and finalized segments in append-only journals.
package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tala/internal/media"
	"tala/internal/models"
)

type Segment struct {
	ID        string  `json:"id"`
	SessionID string  `json:"sessionId"`
	Text      string  `json:"text"`
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	Source    string  `json:"source"`
	Final     bool    `json:"final"`
	Untimed   bool    `json:"untimed,omitempty"`
}
type Session struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Created     time.Time         `json:"created"`
	Duration    float64           `json:"duration"`
	Range       media.TimeRange   `json:"range,omitempty"`
	Type        string            `json:"type"`
	Source      string            `json:"source"`
	MediaPath   string            `json:"mediaPath,omitempty"`
	Status      string            `json:"status"`
	Preview     string            `json:"preview"`
	Corrections map[string]string `json:"corrections"`
	Segments    []Segment         `json:"segments,omitempty"`
	LegacyPath  string            `json:"legacyPath,omitempty"`
}
type Store struct {
	Dir string
	mu  sync.Mutex
}

func ID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

var safeID = regexp.MustCompile(`^[a-f0-9]{24}$`)

func (s *Store) path(id, ext string) (string, error) {
	if !safeID.MatchString(id) {
		return "", errors.New("invalid session ID")
	}
	return filepath.Join(s.Dir, "sessions", id+ext), nil
}
func (s *Store) save(v Session) error {
	p, e := s.path(v.ID, ".json")
	if e != nil {
		return e
	}
	v.Segments = nil
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return models.AtomicWrite(p, b)
}
func (s *Store) Create(kind, title, source string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := Session{ID: ID(), Title: title, Created: time.Now(), Type: kind, Source: source, Status: "processing", Corrections: map[string]string{}}
	return v, s.save(v)
}
func (s *Store) read(id string, segments bool) (Session, error) {
	var v Session
	p, e := s.path(id, ".json")
	if e != nil {
		return v, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal(b, &v); e != nil {
		return v, e
	}
	if v.Corrections == nil {
		v.Corrections = map[string]string{}
	}
	if segments {
		p, _ = s.path(id, ".jsonl")
		f, err := os.Open(p)
		if err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 4096), 1024*1024)
			seen := map[string]bool{}
			for scanner.Scan() {
				var seg Segment
				if json.Unmarshal(scanner.Bytes(), &seg) == nil && !seen[seg.ID] {
					v.Segments = append(v.Segments, seg)
					seen[seg.ID] = true
				}
			}
			if err = scanner.Err(); err != nil {
				return v, err
			}
		} else if !os.IsNotExist(err) {
			return v, err
		}
		sort.SliceStable(v.Segments, func(i, j int) bool { return v.Segments[i].Start < v.Segments[j].Start })
	}
	return v, nil
}
func (s *Store) Get(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(id, true)
}
func (s *Store) List() ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, e := filepath.Glob(filepath.Join(s.Dir, "sessions", "*.json"))
	if e != nil {
		return nil, e
	}
	out := make([]Session, 0, len(files))
	for _, p := range files {
		v, e := s.read(strings.TrimSuffix(filepath.Base(p), ".json"), false)
		if e == nil {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}
func (s *Store) Update(id string, fn func(*Session) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.read(id, false)
	if e != nil {
		return e
	}
	if e = fn(&v); e != nil {
		return e
	}
	return s.save(v)
}
func (s *Store) Append(id string, seg Segment) (Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.path(id, ".jsonl")
	if e != nil {
		return seg, e
	}
	seg.ID = ID()
	seg.SessionID = id
	seg.Final = true
	seg.Text = strings.TrimSpace(seg.Text)
	if seg.End < seg.Start || seg.Start < 0 {
		return seg, errors.New("invalid segment timing")
	}
	b, e := json.Marshal(seg)
	if e != nil {
		return seg, e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if e != nil {
		return seg, e
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		var tail [1]byte
		if _, err = f.ReadAt(tail[:], info.Size()-1); err != nil {
			return seg, err
		}
		if tail[0] != '\n' {
			if _, err = f.Write([]byte("\n")); err != nil {
				return seg, err
			}
		}
	}
	if _, e = f.Write(append(b, '\n')); e != nil {
		return seg, e
	}
	return seg, f.Sync()
}
func (s *Store) Correct(id, segID string, text *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.read(id, true)
	if e != nil {
		return e
	}
	found := false
	for _, seg := range v.Segments {
		if seg.ID == segID {
			found = true
			break
		}
	}
	if !found {
		return errors.New("segment no longer exists")
	}
	if text == nil {
		delete(v.Corrections, segID)
	} else {
		v.Corrections[segID] = *text
	}
	return s.save(v)
}
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.read(id, false)
	if err != nil {
		return err
	}
	if v.LegacyPath != "" {
		skipped, err := s.legacyTombstones()
		if err != nil {
			return err
		}
		skipped[v.LegacyPath] = true
		b, err := json.Marshal(skipped)
		if err != nil {
			return err
		}
		if err = models.AtomicWrite(filepath.Join(s.Dir, "sessions", "legacy-deletions.json"), b); err != nil {
			return err
		}
	}
	p, e := s.path(id, ".json")
	if e != nil {
		return e
	}
	if e = os.Remove(p); e != nil {
		return e
	}
	p, _ = s.path(id, ".jsonl")
	e = os.Remove(p)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func Text(v Session, seg Segment) string {
	if c, ok := v.Corrections[seg.ID]; ok {
		return c
	}
	return strings.TrimSpace(seg.Text)
}
func Clock(seconds float64, sub bool) string {
	ms := int64(seconds*1000 + .5)
	h := ms / 3600000
	m := (ms / 60000) % 60
	s := (ms / 1000) % 60
	if sub {
		return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms%1000)
	}
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
func Export(v Session, format, mode string) (string, error) {
	var b strings.Builder
	sub := format == "srt" || format == "vtt"
	if format == "vtt" {
		b.WriteString("WEBVTT\n\n")
	}
	if format == "md" {
		fmt.Fprintf(&b, "# %s\n\n", strings.ReplaceAll(v.Title, "\n", " "))
	}
	if format != "txt" && format != "md" && !sub {
		return "", errors.New("unsupported export format")
	}
	if mode == "continuous" && !sub {
		var parts []string
		for _, seg := range v.Segments {
			if text := strings.Join(strings.Fields(Text(v, seg)), " "); text != "" {
				parts = append(parts, text)
			}
		}
		b.WriteString(strings.Join(parts, " "))
		return b.String(), nil
	}
	for i, seg := range v.Segments {
		text := Text(v, seg)
		if sub {
			if seg.Untimed || seg.End <= seg.Start {
				return "", errors.New("this transcript has no reliable subtitle timings; export TXT or Markdown")
			}
			start, end := Clock(seg.Start, true), Clock(seg.End, true)
			if format == "srt" {
				start = strings.Replace(start, ".", ",", 1)
				end = strings.Replace(end, ".", ",", 1)
			}
			text = strings.ReplaceAll(strings.ReplaceAll(text, "\r", ""), "\n\n", "\n")
			fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, start, end, text)
			continue
		}
		if !seg.Untimed {
			if mode == "sources" {
				fmt.Fprintf(&b, "[%s · %s]\n", seg.Source, Clock(seg.Start, false))
			} else if mode == "timestamps" {
				fmt.Fprintf(&b, "[%s] ", Clock(seg.Start, false))
			}
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String()) + "\n", nil
}

var legacyLine = regexp.MustCompile(`^\[(.*?)\] \[(\d+:\d+(?::\d+)?) → (\d+:\d+(?::\d+)?)\] (.*)$`)

func parseClock(v string) float64 {
	var n float64
	for _, p := range strings.Split(v, ":") {
		i, _ := strconv.ParseFloat(p, 64)
		n = n*60 + i
	}
	return n
}
func ParseLegacy(text, id string) []Segment {
	var out []Segment
	for _, para := range strings.Split(strings.TrimSpace(text), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		seg := Segment{ID: ID(), SessionID: id, Text: para, Source: "Legacy", Final: true, Untimed: true}
		m := legacyLine.FindStringSubmatch(para)
		if m != nil {
			seg.Source = m[1]
			seg.Start = parseClock(m[2])
			seg.End = parseClock(m[3])
			seg.Text = m[4]
			seg.Untimed = false
		}
		out = append(out, seg)
	}
	return out
}
func (s *Store) ImportLegacy() error {
	skipped, e := s.legacyTombstones()
	if e != nil {
		return e
	}
	existing, e := s.List()
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, v := range existing {
		seen[v.LegacyPath] = true
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "transcript_*.txt"))
	for _, p := range files {
		if seen[p] || skipped[p] {
			continue
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		v, e := s.Create("legacy", strings.TrimSuffix(filepath.Base(p), ".txt"), p)
		if e != nil {
			return e
		}
		segs := ParseLegacy(string(b), v.ID)
		var duration float64
		for _, seg := range segs {
			if _, e = s.Append(v.ID, seg); e != nil {
				return e
			}
			if seg.End > duration {
				duration = seg.End
			}
		}
		if e = s.Update(v.ID, func(v *Session) error {
			v.LegacyPath = p
			v.Status = "completed"
			v.Duration = duration
			if len(segs) > 0 {
				v.Preview = segs[0].Text
			}
			if info, e := os.Stat(p); e == nil {
				v.Created = info.ModTime()
			}
			return nil
		}); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) legacyTombstones() (map[string]bool, error) {
	out := map[string]bool{}
	b, e := os.ReadFile(filepath.Join(s.Dir, "sessions", "legacy-deletions.json"))
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &out); e != nil {
		return nil, e
	}
	return out, nil
}
