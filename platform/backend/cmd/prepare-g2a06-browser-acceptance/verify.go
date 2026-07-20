package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
	"platform.local/capability-platform/backend/internal/testsupport/g2a06acceptance"
	"time"
)

var formalHTTPBase = "http://127.0.0.1:8080"

type browserCompletion struct {
	ReturnURL string `json:"return_url"`
	Code      string `json:"-"`
	State     string `json:"-"`
}
type browserOpen struct {
	CSRFToken  string             `json:"csrf_token"`
	Completion *browserCompletion `json:"completion"`
}
type acceptanceResponse struct {
	status int
	raw    []byte
	cookie *http.Cookie
}

type verifyPersistence func(manifestPayload) error
type verifyStatuses func() (g2a06acceptance.InteractionStatuses, error)

type hostedExchangeResult struct {
	InteractionID string `json:"interaction_id"`
	ResultType    string `json:"result_type"`
	UserSession   *struct {
		AccessToken      string    `json:"access_token"`
		RefreshToken     string    `json:"refresh_token"`
		AccessExpiresAt  time.Time `json:"access_expires_at"`
		RefreshExpiresAt time.Time `json:"refresh_expires_at"`
		User             struct {
			UserID              string  `json:"user_id"`
			AccountStatus       string  `json:"account_status"`
			DisplayName         *string `json:"display_name,omitempty"`
			ProductID           *string `json:"product_id,omitempty"`
			TenantID            *string `json:"tenant_id,omitempty"`
			AccessVersion       *int64  `json:"access_version,omitempty"`
			ProductAccessStatus *string `json:"product_access_status,omitempty"`
			TenantAccessStatus  *string `json:"tenant_access_status,omitempty"`
		} `json:"user"`
	} `json:"user_session,omitempty"`
	AccountResult *struct {
		Result string `json:"result"`
	} `json:"account_result,omitempty"`
}

type hostedProblem struct {
	Type              string `json:"type"`
	Title             string `json:"title"`
	Status            int    `json:"status"`
	Code              string `json:"code"`
	Detail            string `json:"detail,omitempty"`
	RequestID         string `json:"request_id"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds *int   `json:"retry_after_seconds,omitempty"`
	FieldErrors       []struct {
		Field   string `json:"field"`
		Code    string `json:"code"`
		Message string `json:"message,omitempty"`
	} `json:"field_errors,omitempty"`
}

func verifyAcceptance(ctx context.Context, manifest acceptanceManifest, payload manifestPayload, persist verifyPersistence, authStatus verifyStatuses) error {
	if stageRank(payload.Stage) < stageRank(stageWrongVerified) {
		negative, cookie, err := openAcceptance(ctx, manifest.NegativeAuthInteractionID)
		if err != nil {
			return err
		}
		if negative.Completion == nil {
			body, _ := json.Marshal(map[string]any{"identifier": "g2a05-account-acceptance@example.test", "credential": payload.Password, "risk_summary": map[string]any{}})
			response, requestErr := acceptanceRequest(ctx, http.MethodPost, "/api/v1/hosted/interactions/"+manifest.NegativeAuthInteractionID+"/auth/password", body, cookie, negative.CSRFToken, "")
			clear(body)
			if requestErr != nil || response.status != http.StatusOK {
				clear(response.raw)
				return errors.New("complete hidden PKCE interaction")
			}
			var completed browserCompletion
			if json.Unmarshal(response.raw, &completed) != nil || parseCompletion(&completed) != nil {
				clear(response.raw)
				return errors.New("decode hidden PKCE completion")
			}
			clear(response.raw)
			negative.Completion = &completed
		}
		if !sameState(negative.Completion.State, payload.NegativeAuthState) {
			return errors.New("hidden PKCE completion state mismatch")
		}
		if status, raw := exchangeAcceptance(ctx, manifest.NegativeAuthInteractionID, negative.Completion.Code, payload.NegativeCodeVerifier+"wrong", payload.ClientToken); !isStableProblem(status, raw, http.StatusBadRequest, "hosted.pkce_required") {
			clear(raw)
			return errors.New("wrong PKCE verifier did not return the stable rejection")
		} else {
			clear(raw)
		}
		payload.Stage = stageWrongVerified
		if err = persist(payload); err != nil {
			return errors.New("persist wrong PKCE verification")
		}
	}

	if stageRank(payload.Stage) < stageRank(stageReady) {
		positive, err := waitForCompletion(ctx, manifest.AuthInteractionID)
		if err != nil {
			return errors.New("auth interaction did not reach completed state")
		}
		if !sameState(positive.State, payload.AuthState) {
			return errors.New("auth completion state mismatch")
		}
		payload.PositiveCode, payload.PositiveState, payload.Stage = positive.Code, positive.State, stageReady
		if err := persist(payload); err != nil {
			return errors.New("persist positive completion")
		}
	}
	if payload.PositiveCode == "" || !sameState(payload.PositiveState, payload.AuthState) {
		return errors.New("positive completion recovery state is invalid")
	}
	if stageRank(payload.Stage) < stageRank(stageExchanged) {
		status, raw := exchangeAcceptance(ctx, manifest.AuthInteractionID, payload.PositiveCode, payload.CodeVerifier, payload.ClientToken)
		if status == http.StatusOK {
			var result hostedExchangeResult
			if decodeStrict(raw, &result) != nil || result.InteractionID != manifest.AuthInteractionID || result.ResultType != "user_session" || result.UserSession == nil || result.AccountResult != nil || !validIssuedUserSession(result.UserSession, payload) {
				clear(raw)
				return errors.New("exchange token response invalid")
			}
			result.UserSession.AccessToken, result.UserSession.RefreshToken = "", ""
		} else {
			current, statusErr := authStatus()
			if statusErr != nil || current.Auth != hostedinteraction.StatusExchanged {
				clear(raw)
				return errors.New("correct PKCE exchange failed")
			}
		}
		clear(raw)
		payload.Stage = stageExchanged
		if err := persist(payload); err != nil {
			return errors.New("persist successful PKCE exchange")
		}
	}
	if stageRank(payload.Stage) < stageRank(stageReplayVerified) {
		if replay, replayRaw := exchangeAcceptance(ctx, manifest.AuthInteractionID, payload.PositiveCode, payload.CodeVerifier, payload.ClientToken); !isStableProblem(replay, replayRaw, http.StatusConflict, "hosted.invalid_grant") {
			clear(replayRaw)
			return errors.New("authorization code replay did not return the stable rejection")
		} else {
			clear(replayRaw)
		}
		payload.Stage = stageReplayVerified
		if err := persist(payload); err != nil {
			return errors.New("persist authorization code replay verification")
		}
	}
	if stageRank(payload.Stage) < stageRank(stageAccountReady) {
		account, err := waitForCompletion(ctx, manifest.AccountInteractionID)
		if err != nil {
			return errors.New("account interaction did not reach completed state")
		}
		if !sameState(account.State, payload.AccountState) {
			return errors.New("account completion state mismatch")
		}
		payload.AccountCode, payload.AccountCompletionState, payload.Stage = account.Code, account.State, stageAccountReady
		if err = persist(payload); err != nil {
			return errors.New("persist account completion")
		}
	}
	if payload.AccountCode == "" || !sameState(payload.AccountCompletionState, payload.AccountState) {
		return errors.New("account completion recovery state is invalid")
	}
	if stageRank(payload.Stage) < stageRank(stageAccountExchanged) {
		status, raw := exchangeAcceptance(ctx, manifest.AccountInteractionID, payload.AccountCode, "", payload.AccountClientToken)
		if status == http.StatusOK {
			var result hostedExchangeResult
			if decodeStrict(raw, &result) != nil || result.InteractionID != manifest.AccountInteractionID || result.ResultType != "account_completed" || result.UserSession != nil || result.AccountResult == nil || result.AccountResult.Result != "closed" && result.AccountResult.Result != "self_service_completed" {
				clear(raw)
				return errors.New("account completion exchange response invalid")
			}
		} else {
			current, statusErr := authStatus()
			if statusErr != nil || current.Account != hostedinteraction.StatusExchanged {
				clear(raw)
				return errors.New("account completion exchange failed")
			}
		}
		clear(raw)
		payload.Stage = stageAccountExchanged
		if err := persist(payload); err != nil {
			return errors.New("persist account completion exchange")
		}
	}
	if stageRank(payload.Stage) < stageRank(stageAccountReplayVerified) {
		if replay, replayRaw := exchangeAcceptance(ctx, manifest.AccountInteractionID, payload.AccountCode, "", payload.AccountClientToken); !isStableProblem(replay, replayRaw, http.StatusConflict, "hosted.invalid_grant") {
			clear(replayRaw)
			return errors.New("account completion code replay did not return the stable rejection")
		} else {
			clear(replayRaw)
		}
		payload.Stage = stageAccountReplayVerified
		if err := persist(payload); err != nil {
			return errors.New("persist account completion replay verification")
		}
	}
	return nil
}

func validIssuedUserSession(session *struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	User             struct {
		UserID              string  `json:"user_id"`
		AccountStatus       string  `json:"account_status"`
		DisplayName         *string `json:"display_name,omitempty"`
		ProductID           *string `json:"product_id,omitempty"`
		TenantID            *string `json:"tenant_id,omitempty"`
		AccessVersion       *int64  `json:"access_version,omitempty"`
		ProductAccessStatus *string `json:"product_access_status,omitempty"`
		TenantAccessStatus  *string `json:"tenant_access_status,omitempty"`
	} `json:"user"`
}, payload manifestPayload) bool {
	if session.AccessToken == "" || session.RefreshToken == "" || !session.AccessExpiresAt.After(time.Now()) || session.RefreshExpiresAt.Before(session.AccessExpiresAt) || session.User.UserID == "" || session.User.AccountStatus != "active" && session.User.AccountStatus != "locked" && session.User.AccountStatus != "disabled" {
		return false
	}
	if session.User.ProductID != nil && *session.User.ProductID != payload.ProductID || session.User.TenantID != nil && *session.User.TenantID != payload.TenantID {
		return false
	}
	return true
}

func waitForCompletion(ctx context.Context, interactionID string) (*browserCompletion, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		opened, _, _ := openAcceptance(ctx, interactionID)
		if opened.Completion != nil {
			return opened.Completion, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, errors.New("interaction did not reach completed state")
}

func sameState(actual, expected string) bool {
	return actual != "" && expected != "" && len(actual) == len(expected) && subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func decodeStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func isStableProblem(status int, raw []byte, expectedStatus int, expectedCode string) bool {
	var problem hostedProblem
	return status == expectedStatus && decodeStrict(raw, &problem) == nil &&
		problem.Type == "about:blank" && problem.Title == http.StatusText(expectedStatus) &&
		problem.Status == expectedStatus && problem.Code == expectedCode && problem.RequestID != "" &&
		!problem.Retryable && problem.RetryAfterSeconds == nil && len(problem.FieldErrors) == 0
}

func openAcceptance(ctx context.Context, id string) (browserOpen, *http.Cookie, error) {
	response, err := acceptanceRequest(ctx, http.MethodPost, "/api/v1/hosted/interactions/"+id+"/browser-session", nil, nil, "", "")
	if err != nil || response.status != http.StatusOK {
		clear(response.raw)
		return browserOpen{}, nil, errors.New("open hosted browser session")
	}
	var value browserOpen
	if json.Unmarshal(response.raw, &value) != nil {
		clear(response.raw)
		return value, nil, errors.New("decode hosted browser session")
	}
	if value.Completion != nil && parseCompletion(value.Completion) != nil {
		clear(response.raw)
		return value, nil, errors.New("decode hosted completion")
	}
	clear(response.raw)
	return value, response.cookie, nil
}

func parseCompletion(value *browserCompletion) error {
	parsed, err := url.Parse(value.ReturnURL)
	if err != nil {
		return err
	}
	value.Code = parsed.Query().Get("code")
	value.State = parsed.Query().Get("state")
	if value.Code == "" || value.State == "" {
		return errors.New("completion return URL missing code or state")
	}
	return nil
}

func acceptanceRequest(ctx context.Context, method, path string, body []byte, cookie *http.Cookie, csrf, bearer string, idempotencyKey ...string) (acceptanceResponse, error) {
	request, err := http.NewRequestWithContext(ctx, method, formalHTTPBase+path, bytes.NewReader(body))
	if err != nil {
		return acceptanceResponse{}, err
	}
	request.Header.Set("Origin", "https://127.0.0.1:5175")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if len(idempotencyKey) == 1 && idempotencyKey[0] != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey[0])
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return acceptanceResponse{}, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	if readErr != nil {
		return acceptanceResponse{}, readErr
	}
	var cookieOut *http.Cookie
	if cookies := response.Cookies(); len(cookies) > 0 {
		cookieOut = cookies[0]
	}
	return acceptanceResponse{response.StatusCode, raw, cookieOut}, nil
}

func exchangeAcceptance(ctx context.Context, id, code, verifier, token string) (int, []byte) {
	request := map[string]string{"code": code}
	if verifier != "" {
		request["code_verifier"] = verifier
	}
	body, _ := json.Marshal(request)
	response, err := acceptanceRequest(ctx, http.MethodPost, "/api/v1/hosted/interactions/"+id+"/exchange", body, nil, "", token)
	clear(body)
	if err != nil {
		return 0, nil
	}
	return response.status, response.raw
}
