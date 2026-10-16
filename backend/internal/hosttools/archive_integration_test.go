package hosttools

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestArchiveIntegration(t *testing.T) {
	tools := integration(t, "archive")
	api := localAPI{envDefault("DASH_ARCHIVE_URL", "http://127.0.0.1:"+envDefault("DASH_SANDBOX_PORT", "8089"))}
	assertThat(t, nested(api.get("/api/info"), "simulation") == true, "refusing archive acceptance against non-synthetic recordings")
	id := "synthetic-usb-" + time.Now().UTC().Format("150405")
	api.state(t, "parked")
	defer func() { api.request("/api/simulation/state", map[string]string{"state": "unknown"}) }()
	api.post(t, "/api/transfer-destinations", map[string]any{"id": id, "name": "Synthetic archive", "kind": "usb", "volumeUuid": "DASH-SIM-0001", "secret": map[string]any{}}, 200)
	api.post(t, "/api/transfer-destinations/"+id+"/directory", map[string]any{"path": "../escape"}, 400)
	api.post(t, "/api/transfer-destinations/"+id+"/directory", map[string]any{"path": "Test archives/2026"}, 200)
	for _, state := range []string{"unknown", "onroad"} {
		api.state(t, state)
		api.post(t, "/api/transfers", map[string]any{"destinationId": id, "autoResume": true}, 409)
	}
	api.state(t, "parked")
	raw := api.post(t, "/api/transfers", map[string]any{"destinationId": id, "autoResume": true}, 202)
	var batch struct {
		ID string `json:"id"`
	}
	require(t, json.Unmarshal(raw, &batch))
	find := func() map[string]any {
		snapshot := api.get("/api/transfers")
		if snapshot == nil {
			return nil
		}
		rows, _ := snapshot["batches"].([]any)
		for _, row := range rows {
			value, _ := row.(map[string]any)
			if value["id"] == batch.ID {
				return value
			}
		}
		return nil
	}
	api.post(t, "/api/transfers/"+batch.ID+"/pause", map[string]any{}, 200)
	waitFor(t, func() bool { return nested(find(), "status") == "paused" })
	if os.Getenv("DASH_ARCHIVE_RESTART") == "1" {
		assertThat(t, api.base == "http://127.0.0.1:"+envDefault("DASH_SANDBOX_PORT", "8089"), "container restart is restricted to the local sandbox")
		output(t, tools, "docker", "compose", "restart", "dash")
		waitFor(t, func() bool { return nested(api.get("/api/info"), "simulation") == true })
		assertThat(t, nested(find(), "status") == "paused", "manual pause did not survive server restart")
	}
	before := nested(find(), "completedFiles")
	api.post(t, "/api/transfer-destinations/"+id+"/directory", map[string]any{"path": "Changed"}, 409)
	api.state(t, "onroad")
	api.post(t, "/api/transfers/"+batch.ID+"/resume", map[string]any{}, 409)
	time.Sleep(300 * time.Millisecond)
	assertThat(t, nested(find(), "completedFiles") == before, "archive progressed onroad")
	api.state(t, "parked")
	api.post(t, "/api/transfers/"+batch.ID+"/resume", map[string]any{}, 200)
	waitFor(t, func() bool {
		status, _ := nested(find(), "status").(string)
		if status == "needs_attention" || status == "complete_with_issues" {
			t.Fatalf("synthetic archive failed: %v", find())
		}
		return status == "complete"
	})
	result := find()
	total, _ := result["totalFiles"].(float64)
	assertThat(t, total >= 36 && result["completedFiles"] == total && result["completedBytes"] == result["totalBytes"], "archive missed original cameras/logs")
	assertThat(t, result["directory"] == "Test archives/2026" && result["progressReady"] == true && result["transferredBytes"] == result["totalBytes"], "archive folder or durable progress differs")
	_, public, err := api.request("/api/transfers", nil)
	require(t, err)
	for _, private := range []string{"uploadUrl", "refreshToken", "accessToken", "secretKey", "clientSecret", "setup.key"} {
		assertThat(t, !strings.Contains(string(public), private), "private transfer state leaked")
	}
	// The disposable index remains independent of the completed archive record.
	api.post(t, "/api/index/rebuild", map[string]any{}, 200)
	assertThat(t, nested(find(), "status") == "complete", "index rebuild reset archive state")
	api.post(t, "/api/transfer-destinations/"+id+"/remove", map[string]any{}, 200)
}
