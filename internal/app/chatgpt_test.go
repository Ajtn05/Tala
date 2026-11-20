package app

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestChatGPTPlanAccountLifecycle(t *testing.T) {
	keys, root := testMeetingKeys{}, t.TempDir()
	plan := loadChatGPTPlan(keys, root)
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const clientID = "oaiapp_test_registration"
	const access, refresh, rotated = "fixture-access-token", "fixture-refresh-token", "fixture-rotated-token"
	var authorization url.Values
	refreshes, revocations := 0, 0
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plan.client.Transport = meetingTestTransport(func(req *http.Request) (*http.Response, error) {
		var body any
		switch req.URL.String() {
		case chatGPTTokenURL:
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if req.Form.Get("client_id") != clientID || req.Form.Get("resource") != chatGPTResource {
				t.Fatal("token exchange used the wrong client or resource")
			}
			switch req.Form.Get("grant_type") {
			case "authorization_code":
				digest := sha256.Sum256([]byte(req.Form.Get("code_verifier")))
				if authorization.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) ||
					req.Form.Get("redirect_uri") != authorization.Get("redirect_uri") || req.Form.Get("code") != "fixture-code" {
					t.Fatal("token exchange did not use the callback and PKCE challenge")
				}
				body = map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600,
					"scope":    "openid offline_access " + chatGPTPlanScope,
					"id_token": testChatGPTIdentity(t, signingKey, clientID, authorization.Get("nonce"))}
			case "refresh_token":
				refreshes++
				if req.Form.Get("refresh_token") != refresh || req.Form.Get("scope") != "" {
					t.Fatal("refresh used the wrong grant")
				}
				body = map[string]any{"access_token": rotated, "refresh_token": "replacement-refresh", "token_type": "Bearer", "expires_in": 3600}
			default:
				t.Fatal("unexpected OAuth grant")
			}
		case chatGPTJWKS:
			body = map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "fixture", "alg": "RS256", "use": "sig", "e": "AQAB",
				"n": base64.RawURLEncoding.EncodeToString(signingKey.N.Bytes())}}}
		case chatGPTResource + "/models":
			if req.Method != http.MethodGet || req.Header.Get("Authorization") != "Bearer "+access {
				t.Fatal("model discovery did not use the signed-in account")
			}
			body = map[string]any{"models": []map[string]string{
				{"slug": "hidden", "visibility": "hidden"}, {"slug": "first-model", "display_name": "First model", "visibility": "list"},
				{"slug": "second-model", "display_name": "Second model", "visibility": "list"},
			}}
		case chatGPTIssuer + "/.well-known/openid-configuration":
			body = map[string]string{"revocation_endpoint": chatGPTIssuer + "/api/accounts/oauth/revoke"}
		case chatGPTIssuer + "/api/accounts/oauth/revoke":
			revocations++
			if err := req.ParseForm(); err != nil || req.Form.Get("token") != "replacement-refresh" || req.Form.Get("client_id") != clientID {
				t.Fatal("sign-out did not revoke the active refresh token")
			}
		default:
			t.Fatalf("unexpected outbound URL: %s", req.URL)
		}
		result, _ := json.Marshal(body)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(result)))}, nil
	})
	selected, welcome, notice, err := plan.signIn(ctx, "", func(raw string) error {
		browser, err := url.Parse(raw)
		if err != nil {
			return err
		}
		authorization = browser.Query()
		if browser.Scheme != "https" || browser.Host != "auth.openai.com" || authorization.Get("client_id") != chatGPTNewClient ||
			authorization.Get("agent_name_hint") != "Tala" || !strings.HasPrefix(authorization.Get("ext_agent_host_id"), "urn:uuid:") ||
			authorization.Get("code_challenge_method") != "S256" || !strings.Contains(authorization.Get("scope"), chatGPTPlanScope) {
			t.Fatal("authorization URL does not identify Tala or request the required permissions")
		}
		callback := authorization.Get("redirect_uri")
		query := url.Values{"code": {"fixture-code"}, "client_id": {clientID}, "state": {"invalid-state"}}
		for _, code := range []int{http.StatusBadRequest, http.StatusOK} {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, callback+"?"+query.Encode(), nil)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			res.Body.Close()
			if res.StatusCode != code {
				t.Errorf("callback returned %d, want %d", res.StatusCode, code)
			}
			query.Set("state", authorization.Get("state"))
		}
		return nil
	})
	if err != nil || selected != clientID || !welcome || notice != "" {
		t.Fatalf("sign-in = %q, %v, %q, %v", selected, welcome, notice, err)
	}
	accounts := plan.accounts()
	if len(accounts) != 1 || !accounts[0].Authorized || len(accounts[0].Models) != 2 || accounts[0].Models[0].ID != "first-model" {
		t.Fatalf("account model list = %+v", accounts)
	}
	public, _ := json.Marshal(accounts)
	if strings.Contains(string(public), access) || strings.Contains(string(public), refresh) {
		t.Fatal("session credentials escaped the Keychain-backed connection")
	}
	plan.state.Profiles[0].Tokens.Expiry = time.Now().Add(-time.Minute)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if token, err := plan.accessToken(ctx, selected); err != nil || token != rotated {
				t.Errorf("session renewal failed: %v", err)
			}
		})
	}
	wg.Wait()
	if refreshes != 1 {
		t.Fatalf("rotating token refreshed %d times", refreshes)
	}
	if _, err := plan.signOut(ctx, selected); err != nil || revocations != 1 {
		t.Fatalf("sign-out = %v, %d revocations", err, revocations)
	}
	reloaded := loadChatGPTPlan(keys, root)
	if view := reloaded.accounts(); len(view) != 1 || view[0].SignedIn || view[0].ID != selected || reloaded.state.HostID != plan.state.HostID {
		t.Fatalf("sign-out lost registration or retained keys: %+v", view)
	}
}

func testChatGPTIdentity(t *testing.T, key *rsa.PrivateKey, clientID, nonce string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"fixture"}`))
	claims, _ := json.Marshal(map[string]any{"iss": chatGPTIssuer, "aud": clientID, "sub": "fixture-user", "nonce": nonce, "email": "fixture@example.com",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()})
	input := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestChatGPTInferenceRequiresAuthorizationAndCompletedStream(t *testing.T) {
	const selected = "oaiapp_test_registration"
	plan := loadChatGPTPlan(testMeetingKeys{}, t.TempDir())
	plan.state.Profiles = []chatGPTProfile{{ClientID: selected, Tokens: chatGPTTokens{AccessToken: "account-token", Expiry: time.Now().Add(time.Hour)}}}
	requests := 0
	output := `{"type":"response.output_text.delta","delta":"{}"}`
	client := &http.Client{Transport: meetingTestTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() != chatGPTResource+"/responses" || req.Header.Get("Authorization") != "Bearer account-token" {
			t.Fatal("inference used the wrong route or account")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if string(body["store"]) != "false" || string(body["stream"]) != "true" || body["max_output_tokens"] != nil ||
			!strings.Contains(string(body["input"]), "fixture transcript") || !strings.Contains(string(body["text"]), "json_schema") {
			t.Fatal("request did not use the supported ChatGPT plan protocol")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(output))}, nil
	})}
	complete := plan.complete(client, meetingTarget{Account: selected, Model: "first-model"})
	run := func() (string, error) {
		return complete(context.Background(), "notes", "instructions", "fixture transcript", meetingDigestSchema())
	}
	if _, err := run(); !errors.Is(err, errChatGPTScope) || requests != 0 {
		t.Fatalf("inference ran without permission: %v", err)
	}
	plan.state.Profiles[0].Tokens.Scope = chatGPTPlanScope
	delta := fmt.Sprintf("data: %s\n\n", output)
	output = delta
	if _, err := run(); err == nil {
		t.Fatal("accepted text without a completed event")
	}
	output = delta + "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"subscription_sharing_usage_limit_exceeded\"}}}\n\n"
	if _, err := run(); !errors.Is(err, errChatGPTLimit) {
		t.Fatalf("usage limit translated to %v", err)
	}
	longOutput := `{"overview":"` + strings.Repeat("Detailed meeting notes. ", 600) + `"}`
	event, err := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": longOutput})
	if err != nil {
		t.Fatal(err)
	}
	output = "data: " + string(event) + "\n\ndata: {\"type\":\"response.completed\"}\n\n"
	if got, err := run(); err != nil || got != longOutput {
		t.Fatalf("completed output = %d bytes, %v", len(got), err)
	}
}
