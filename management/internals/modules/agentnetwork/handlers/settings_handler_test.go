package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/netbirdio/netbird/shared/management/http/api"
)

// TestSettingsHandler_GetUnbootstrappedReturns404 pins the OpenAPI contract:
// an account with no settings row answers a plain 404, never 200 with a
// null/zero body, so API clients can rely on the status code alone.
func TestSettingsHandler_GetUnbootstrappedReturns404(t *testing.T) {
	f := newAgentNetworkHandlerFixture(t)

	rec := f.do(t, http.MethodGet, "/agent-network/settings", "")
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"unbootstrapped account must read as 404: got %d body=%s", rec.Code, rec.Body.String())
	assert.NotEqual(t, "null", trimSpace(rec.Body.String()),
		"the legacy 200+null shape must not come back")
}

// TestSettingsHandler_PutBootstrapsWithCluster covers the settings-first
// bootstrap path: a PUT carrying a cluster on an unbootstrapped account
// creates the row (cluster pinned, subdomain assigned, log collection
// defaulted on) and applies the toggles from the same request.
func TestSettingsHandler_PutBootstrapsWithCluster(t *testing.T) {
	f := newAgentNetworkHandlerFixture(t)

	rec := f.do(t, http.MethodPut, "/agent-network/settings",
		`{"cluster": "eu.proxy.netbird.io", "enable_prompt_collection": true}`)
	require.Equal(t, http.StatusOK, rec.Code, "bootstrap PUT must succeed: %s", rec.Body.String())

	var got api.AgentNetworkSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "eu.proxy.netbird.io", got.Cluster, "cluster must be pinned from the request")
	assert.NotEmpty(t, got.Subdomain, "subdomain must be assigned at bootstrap")
	assert.Equal(t, got.Subdomain+".eu.proxy.netbird.io", got.Endpoint, "endpoint must combine subdomain and cluster")
	assert.True(t, got.EnableLogCollection, "log collection must default on at bootstrap")
	assert.True(t, got.EnablePromptCollection, "toggle from the bootstrap request must apply")
	require.NotNil(t, got.AccessLogRetentionDays)
	assert.Equal(t, 30, *got.AccessLogRetentionDays, "retention must default at bootstrap")

	// The row is now readable via GET.
	rec = f.do(t, http.MethodGet, "/agent-network/settings", "")
	require.Equal(t, http.StatusOK, rec.Code, "GET after bootstrap must succeed")
}

// TestSettingsHandler_PutWithoutClusterOnUnbootstrapped pins that a toggle-only
// PUT cannot conjure a settings row out of nothing — there is no cluster to
// pin — and surfaces as 404 like the GET.
func TestSettingsHandler_PutWithoutClusterOnUnbootstrapped(t *testing.T) {
	f := newAgentNetworkHandlerFixture(t)

	rec := f.do(t, http.MethodPut, "/agent-network/settings", `{"enable_log_collection": false}`)
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"toggle-only PUT on an unbootstrapped account must 404: got %d body=%s", rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "cluster",
		"the error must point the caller at the bootstrap paths: %s", rec.Body.String())
}

// TestSettingsHandler_PutPartialUpdatePreservesOmitted is the merge-semantics
// guard: omitted fields keep their stored values, so a client managing a
// single toggle can never clobber account-level configuration it did not send.
func TestSettingsHandler_PutPartialUpdatePreservesOmitted(t *testing.T) {
	f := newAgentNetworkHandlerFixture(t)

	rec := f.do(t, http.MethodPut, "/agent-network/settings",
		`{"cluster": "eu.proxy.netbird.io", "enable_prompt_collection": true, "redact_pii": true, "access_log_retention_days": 14}`)
	require.Equal(t, http.StatusOK, rec.Code, "bootstrap PUT must succeed: %s", rec.Body.String())

	// Flip one field; everything else must survive.
	rec = f.do(t, http.MethodPut, "/agent-network/settings", `{"enable_log_collection": false}`)
	require.Equal(t, http.StatusOK, rec.Code, "partial PUT must succeed: %s", rec.Body.String())

	var got api.AgentNetworkSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.False(t, got.EnableLogCollection, "sent toggle must apply")
	assert.True(t, got.EnablePromptCollection, "omitted prompt collection must be preserved")
	assert.True(t, got.RedactPii, "omitted redact_pii must be preserved")
	require.NotNil(t, got.AccessLogRetentionDays)
	assert.Equal(t, 14, *got.AccessLogRetentionDays, "omitted retention must be preserved")

	// An empty body is a no-op update.
	rec = f.do(t, http.MethodPut, "/agent-network/settings", `{}`)
	require.Equal(t, http.StatusOK, rec.Code, "empty PUT must succeed: %s", rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.True(t, got.EnablePromptCollection, "no-op PUT must not disturb stored values")
}

// TestSettingsHandler_PutRejectsClusterChange pins cluster immutability: once
// assigned, a differing cluster is rejected as a validation error instead of
// being silently ignored, so callers never observe a value other than the one
// they sent. Echoing the assigned cluster back stays valid, which lets
// declarative clients send their full desired state idempotently.
func TestSettingsHandler_PutRejectsClusterChange(t *testing.T) {
	f := newAgentNetworkHandlerFixture(t)

	rec := f.do(t, http.MethodPut, "/agent-network/settings", `{"cluster": "eu.proxy.netbird.io"}`)
	require.Equal(t, http.StatusOK, rec.Code, "bootstrap PUT must succeed: %s", rec.Body.String())

	rec = f.do(t, http.MethodPut, "/agent-network/settings", `{"cluster": "us.proxy.netbird.io"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"cluster change must be rejected as a validation error: got %d body=%s", rec.Code, rec.Body.String())

	rec = f.do(t, http.MethodPut, "/agent-network/settings",
		`{"cluster": "eu.proxy.netbird.io", "redact_pii": true}`)
	require.Equal(t, http.StatusOK, rec.Code, "echoing the assigned cluster must stay valid: %s", rec.Body.String())

	var got api.AgentNetworkSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "eu.proxy.netbird.io", got.Cluster, "cluster must be unchanged")
	assert.True(t, got.RedactPii, "toggle sent alongside the echoed cluster must apply")
}
