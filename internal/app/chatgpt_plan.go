package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

const chatGPTMeetingProvider = "chatgpt"

type chatGPTModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type chatGPTProfile struct {
	ClientID string         `json:"clientId"`
	Subject  string         `json:"subject"`
	Email    string         `json:"email"`
	Models   []chatGPTModel `json:"models,omitempty"`
	Tokens   chatGPTTokens  `json:"tokens"`
}

type chatGPTRegistration struct {
	HostID   string           `json:"hostId"`
	Welcomed bool             `json:"welcomed,omitempty"`
	Profiles []chatGPTProfile `json:"profiles,omitempty"`
}

type chatGPTAccount struct {
	ID         string         `json:"id"`
	Label      string         `json:"label"`
	SignedIn   bool           `json:"signedIn"`
	Authorized bool           `json:"authorized"`
	Models     []chatGPTModel `json:"models"`
}

type chatGPTPlan struct {
	mu      sync.Mutex
	keys    meetingKeyStore
	item    string
	client  *http.Client
	state   chatGPTRegistration
	loadErr error
}

func loadChatGPTPlan(keys meetingKeyStore, root string) *chatGPTPlan {
	if path, err := filepath.EvalSymlinks(root); err == nil {
		root = path
	}
	if path, err := filepath.Abs(root); err == nil {
		root = path
	}
	// Tala holds studio.lock for this data root. Different roots use different
	// Keychain items, so separate processes cannot race rotating credentials.
	p := &chatGPTPlan{keys: keys, item: fmt.Sprintf("chatgpt-plan:%x", sha256.Sum256([]byte(root))), client: &http.Client{
		Timeout: 15 * time.Second, CheckRedirect: meetingCloudClient.CheckRedirect,
	}}
	if !keys.Has(p.item) {
		return p
	}
	data, err := keys.Load(p.item)
	if err != nil || json.Unmarshal([]byte(data), &p.state) != nil || p.state.HostID == "" {
		p.state = chatGPTRegistration{}
		p.loadErr = errors.New("could not read Tala’s ChatGPT accounts from macOS Keychain")
	}
	return p
}

func (p *chatGPTPlan) save(state chatGPTRegistration) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := p.keys.Save(p.item, string(data)); err != nil {
		return err
	}
	p.state = state
	return nil
}

func (p *chatGPTPlan) accounts() []chatGPTAccount {
	p.mu.Lock()
	defer p.mu.Unlock()
	accounts := make([]chatGPTAccount, 0, len(p.state.Profiles))
	for i, profile := range p.state.Profiles {
		label := fmt.Sprintf("Account %d", i+1)
		if profile.Email != "" {
			label = profile.Email + " · " + label
		}
		accounts = append(accounts, chatGPTAccount{
			ID: profile.ClientID, Label: label, SignedIn: profile.Tokens.AccessToken != "",
			Authorized: profile.Tokens.AccessToken != "" && profile.Tokens.allowsPlan(), Models: slices.Clone(profile.Models),
		})
	}
	return accounts
}

func (p *chatGPTPlan) index(clientID string) int {
	return slices.IndexFunc(p.state.Profiles, func(profile chatGPTProfile) bool { return profile.ClientID == clientID })
}

func (p *chatGPTPlan) signIn(ctx context.Context, existing string, openBrowser func(string) error) (clientID string, welcome bool, notice string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loadErr != nil {
		return "", false, "", p.loadErr
	}
	clientID = chatGPTNewClient
	index := p.index(existing)
	if existing != "" {
		if index < 0 {
			return "", false, "", errors.New("choose a saved ChatGPT account")
		}
		clientID = existing
	}
	if p.state.HostID == "" {
		var id [16]byte
		rand.Read(id[:])
		id[6] = (id[6] & 0x0f) | 0x40
		id[8] = (id[8] & 0x3f) | 0x80
		state := p.state
		state.HostID = fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
		if err := p.save(state); err != nil {
			return "", false, "", err
		}
	}
	reconsent := index >= 0 && p.state.Profiles[index].Tokens.AccessToken != "" && !p.state.Profiles[index].Tokens.allowsPlan()
	auth, code, err := authorizeChatGPT(ctx, clientID, p.state.HostID, reconsent, openBrowser)
	if err != nil {
		return "", false, "", err
	}
	tokens, err := p.exchange(ctx, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {code.clientID}, "code": {code.code},
		"code_verifier": {auth.verifier}, "redirect_uri": {auth.redirectURI},
	})
	if err != nil {
		return "", false, "", err
	}
	subject, email, err := p.verifyIdentity(ctx, code.clientID, auth.nonce, tokens)
	if err != nil {
		return "", false, "", err
	}
	if index >= 0 && p.state.Profiles[index].Subject != subject || index < 0 && p.index(code.clientID) >= 0 {
		return "", false, "", errors.New("ChatGPT sign-in did not match the selected account")
	}
	profile := chatGPTProfile{ClientID: code.clientID, Subject: subject, Email: email, Tokens: tokens}
	if ctx.Err() != nil {
		return "", false, "", ctx.Err()
	}
	state := p.state
	state.Profiles = slices.Clone(state.Profiles)
	if index < 0 {
		state.Profiles = append(state.Profiles, profile)
		index = len(state.Profiles) - 1
	} else {
		state.Profiles[index] = profile
	}
	if err := p.save(state); err != nil {
		return "", false, "", err
	}
	if !tokens.allowsPlan() {
		return profile.ClientID, false, errChatGPTScope.Error(), nil
	}
	models, err := p.listModels(ctx, tokens.AccessToken)
	if err != nil {
		return profile.ClientID, false, err.Error(), nil
	}
	state = p.state
	state.Profiles = slices.Clone(state.Profiles)
	state.Profiles[index].Models = models
	welcome = !state.Welcomed
	state.Welcomed = true
	if err := p.save(state); err != nil {
		return profile.ClientID, false, "", err
	}
	return profile.ClientID, welcome, "", nil
}

func (p *chatGPTPlan) accessToken(ctx context.Context, clientID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accessTokenLocked(ctx, clientID)
}

func (p *chatGPTPlan) accessTokenLocked(ctx context.Context, clientID string) (string, error) {
	index := p.index(clientID)
	if index < 0 || p.state.Profiles[index].Tokens.AccessToken == "" {
		return "", errChatGPTSignIn
	}
	old := p.state.Profiles[index].Tokens
	if !old.allowsPlan() {
		return "", errChatGPTScope
	}
	if time.Until(old.Expiry) > time.Minute || (old.RefreshToken == "" && time.Now().Before(old.Expiry)) {
		return old.AccessToken, nil
	}
	if old.RefreshToken == "" {
		if err := p.saveTokens(index, chatGPTTokens{}); err != nil {
			return "", err
		}
		return "", errChatGPTSignIn
	}
	tokens, err := p.exchange(ctx, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {old.RefreshToken},
	})
	if err != nil {
		if errors.Is(err, errChatGPTSignIn) {
			if clearErr := p.saveTokens(index, chatGPTTokens{}); clearErr != nil {
				return "", clearErr
			}
		}
		return "", err
	}
	if tokens.RefreshToken == "" {
		return "", errors.New("OpenAI did not return the renewed ChatGPT session. Sign in again")
	}
	// Only retain the ID token whose nonce and identity we verified at sign-in.
	tokens.IDToken = old.IDToken
	if tokens.Scope == "" {
		tokens.Scope = old.Scope // OAuth omits scope when the grant is unchanged.
	}
	if err := p.saveTokens(index, tokens); err != nil {
		return "", err
	}
	if !tokens.allowsPlan() {
		return "", errChatGPTScope
	}
	return tokens.AccessToken, nil
}

func (p *chatGPTPlan) saveTokens(index int, tokens chatGPTTokens) error {
	state := p.state
	state.Profiles = slices.Clone(state.Profiles)
	state.Profiles[index].Tokens = tokens
	if !tokens.allowsPlan() {
		state.Profiles[index].Models = nil
	}
	return p.save(state)
}

func (p *chatGPTPlan) refreshModels(ctx context.Context, clientID string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	token, err := p.accessTokenLocked(ctx, clientID)
	if err != nil {
		return false, err
	}
	list, err := p.listModels(ctx, token)
	if err != nil {
		return false, err
	}
	state := p.state
	state.Profiles = slices.Clone(state.Profiles)
	state.Profiles[p.index(clientID)].Models = list
	welcome := !state.Welcomed
	state.Welcomed = true
	return welcome, p.save(state)
}

func (p *chatGPTPlan) signOut(ctx context.Context, clientID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.index(clientID)
	if index < 0 {
		return "", errors.New("choose a saved ChatGPT account")
	}
	profile := p.state.Profiles[index]
	notice := ""
	if profile.Tokens.RefreshToken != "" && !p.revoke(ctx, profile) {
		notice = "Signed out in Tala. To revoke the session online, disconnect Tala in ChatGPT settings."
	}
	return notice, p.saveTokens(index, chatGPTTokens{})
}

func (p *chatGPTPlan) revoke(ctx context.Context, profile chatGPTProfile) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chatGPTIssuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return false
	}
	res, err := p.client.Do(req)
	if err != nil {
		return false
	}
	var metadata struct {
		Revocation string `json:"revocation_endpoint"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 64*1024)).Decode(&metadata)
	res.Body.Close()
	endpoint, parseErr := url.Parse(metadata.Revocation)
	if err != nil || res.StatusCode != http.StatusOK || parseErr != nil || endpoint.Scheme != "https" || endpoint.Host != "auth.openai.com" || endpoint.User != nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(time.Duration(i) * 250 * time.Millisecond):
			}
		}
		res, err := p.authRequest(ctx, endpoint.String(), url.Values{
			"token": {profile.Tokens.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {profile.ClientID},
		})
		if err == nil {
			res.Body.Close()
			if res.StatusCode < 500 {
				return res.StatusCode == http.StatusOK
			}
		}
	}
	return false
}
