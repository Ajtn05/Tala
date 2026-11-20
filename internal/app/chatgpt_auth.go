package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	chatGPTIssuer    = "https://auth.openai.com"
	chatGPTAuthorize = chatGPTIssuer + "/api/accounts/authorize"
	chatGPTTokenURL  = chatGPTIssuer + "/api/accounts/oauth/token"
	chatGPTJWKS      = chatGPTIssuer + "/.well-known/jwks.json"
	chatGPTResource  = "https://api.openai.com/v1"
	chatGPTPlanScope = "chatgpt.tokens.use.direct"
	chatGPTNewClient = "dynamic_agent_client"
	chatGPTCallback  = "/auth/callback"
)

var (
	errChatGPTSignIn = errors.New("sign in to ChatGPT again in Meeting notes settings")
	errChatGPTScope  = errors.New("allow ChatGPT plan usage when you sign in, or choose another provider")
	errChatGPTLimit  = errors.New("ChatGPT plan usage limit reached")
)

type chatGPTTokens struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

func (t chatGPTTokens) allowsPlan() bool {
	for _, scope := range strings.Fields(t.Scope) {
		if scope == chatGPTPlanScope {
			return true
		}
	}
	return false
}

type chatGPTAuthorization struct {
	state, nonce, verifier string
	clientID, redirectURI  string
}

func newChatGPTAuthorization(clientID, redirectURI string) chatGPTAuthorization {
	random := func() string {
		var b [32]byte
		rand.Read(b[:])
		return base64.RawURLEncoding.EncodeToString(b[:])
	}
	return chatGPTAuthorization{state: random(), nonce: random(), verifier: random(), clientID: clientID, redirectURI: redirectURI}
}

func (a chatGPTAuthorization) browserURL(hostID string, reconsent bool) string {
	challenge := sha256.Sum256([]byte(a.verifier))
	params := url.Values{
		"client_id": {a.clientID}, "ext_agent_host_id": {hostID}, "redirect_uri": {a.redirectURI},
		"response_type": {"code"}, "scope": {"openid profile email offline_access resource.invoke " + chatGPTPlanScope},
		"resource": {chatGPTResource}, "state": {a.state}, "nonce": {a.nonce},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])},
	}
	if a.clientID == chatGPTNewClient {
		params.Set("agent_name_hint", "Tala")
	} else if reconsent {
		params.Set("prompt", "consent")
	}
	return chatGPTAuthorize + "?" + params.Encode()
}

type chatGPTCode struct {
	code, clientID string
	err            error
}

func (a chatGPTAuthorization) callback(results chan<- chatGPTCode) http.Handler {
	var received atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodGet || r.URL.Path != chatGPTCallback || "http://"+r.Host+chatGPTCallback != a.redirectURI {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		if subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(a.state)) != 1 || !received.CompareAndSwap(false, true) {
			http.Error(w, "This sign-in callback is no longer valid. Return to Tala.", http.StatusBadRequest)
			return
		}
		result := chatGPTCode{code: query.Get("code"), clientID: query.Get("client_id")}
		switch {
		case query.Get("error") != "":
			result.err = errors.New("ChatGPT sign-in was declined or did not complete")
		case result.code == "":
			result.err = errors.New("ChatGPT returned an incomplete sign-in")
		case a.clientID != chatGPTNewClient:
			if result.clientID != "" && result.clientID != a.clientID {
				result.err = errors.New("ChatGPT returned a different account registration")
			}
			result.clientID = a.clientID
		case !strings.HasPrefix(result.clientID, "oaiapp_") || !meetingModelID.MatchString(result.clientID):
			result.err = errors.New("ChatGPT did not issue an app registration")
		}
		fmt.Fprintln(w, "Return to Tala to finish signing in.")
		results <- result
	})
}

func authorizeChatGPT(ctx context.Context, clientID, hostID string, reconsent bool, openBrowser func(string) error) (chatGPTAuthorization, chatGPTCode, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return chatGPTAuthorization{}, chatGPTCode{}, errors.New("could not start the local ChatGPT sign-in callback")
	}
	defer listener.Close()
	auth := newChatGPTAuthorization(clientID, "http://"+listener.Addr().String()+chatGPTCallback)
	results := make(chan chatGPTCode, 1)
	server := &http.Server{Handler: auth.callback(results), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 16 * 1024}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
	}()
	go server.Serve(listener)
	if err := openBrowser(auth.browserURL(hostID, reconsent)); err != nil {
		return auth, chatGPTCode{}, errors.New("could not open the browser for ChatGPT sign-in")
	}
	select {
	case result := <-results:
		return auth, result, result.err
	case <-ctx.Done():
		return auth, chatGPTCode{}, ctx.Err()
	}
}

func (p *chatGPTPlan) exchange(ctx context.Context, params url.Values) (chatGPTTokens, error) {
	params.Set("resource", chatGPTResource)
	res, err := p.authRequest(ctx, chatGPTTokenURL, params)
	if err != nil {
		return chatGPTTokens{}, err
	}
	defer res.Body.Close()
	var result struct {
		chatGPTTokens
		TokenType string `json:"token_type"`
		ExpiresIn int64  `json:"expires_in"`
		Error     string `json:"error"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 96*1024)).Decode(&result)
	if res.StatusCode != http.StatusOK {
		switch result.Error {
		case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
			return chatGPTTokens{}, errChatGPTSignIn
		}
		return chatGPTTokens{}, fmt.Errorf("OpenAI could not complete ChatGPT sign-in (HTTP %d)", res.StatusCode)
	}
	if err != nil || result.AccessToken == "" || !strings.EqualFold(result.TokenType, "Bearer") || result.ExpiresIn <= 0 || result.ExpiresIn > 86400 {
		return chatGPTTokens{}, errors.New("OpenAI returned an incomplete ChatGPT session")
	}
	result.Expiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	return result.chatGPTTokens, nil
}

func (p *chatGPTPlan) authRequest(ctx context.Context, endpoint string, params url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, errors.New("could not prepare the ChatGPT sign-in request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not reach OpenAI for ChatGPT sign-in")
	}
	return res, nil
}

func (p *chatGPTPlan) verifyIdentity(ctx context.Context, clientID, nonce string, tokens chatGPTTokens) (subject, email string, err error) {
	ctx = oidc.ClientContext(ctx, p.client)
	keys := oidc.NewRemoteKeySet(ctx, chatGPTJWKS)
	verifier := oidc.NewVerifier(chatGPTIssuer, keys, &oidc.Config{ClientID: clientID, SupportedSigningAlgs: []string{oidc.RS256}})
	id, err := verifier.Verify(ctx, tokens.IDToken)
	var claims struct {
		Email           string `json:"email"`
		AuthorizedParty string `json:"azp"`
	}
	if err != nil || id.Subject == "" || id.Nonce != nonce || id.Claims(&claims) != nil ||
		((len(id.Audience) > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != clientID) ||
		(id.AccessTokenHash != "" && id.VerifyAccessToken(tokens.AccessToken) != nil) {
		return "", "", errors.New("could not verify the ChatGPT account. Try signing in again")
	}
	return id.Subject, claims.Email, nil
}
