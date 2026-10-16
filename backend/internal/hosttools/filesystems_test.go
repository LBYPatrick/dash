package hosttools

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
	"testing"
)

func TestReleaseRegistrationFromCDEnvironment(t *testing.T) {
	t.Setenv("DASH_GOOGLE_CLIENT_ID", "test-google-app")
	t.Setenv("DASH_GOOGLE_CLIENT_SECRET", "test-client-secret")
	t.Setenv("DASH_MICROSOFT_CLIENT_ID", "test-microsoft-app")
	encoded, err := releaseRegistration()
	require(t, err)
	data, err := base64.RawStdEncoding.DecodeString(encoded)
	require(t, err)
	var value map[string]string
	require(t, json.Unmarshal(data, &value))
	assertThat(t, value["googleClientId"] == "test-google-app" && value["googleClientSecret"] == "test-client-secret", "CD registration was not embedded")
	t.Setenv("DASH_GOOGLE_CLIENT_SECRET", "")
	_, err = releaseRegistration()
	if err == nil {
		t.Fatal("accepted incomplete OAuth registration")
	}
}
func TestFilesystemIntegration(t *testing.T) {
	tools := integration(t, "filesystem")
	if runtime.GOOS != "linux" {
		t.Fatal("run filesystem-smoke on a Linux build host; the device must never run this test")
	}
	arch := runtime.GOARCH
	bundle, err := tools.BuildFilesystems(arch)
	require(t, err)
	binary := tools.path(".artifacts/archive-filesystem.test")
	data, err := tools.command(tools.path("backend"), []string{"CGO_ENABLED=0", "GOMAXPROCS=2"}, "go", "test", "-c", "-o", binary, "./internal/archive")
	if err != nil {
		t.Fatalf("filesystem test build: %v %s", err, data)
	}
	output(t, tools, "sudo", "unshare", "--mount", "--propagation", "private", "env", "DASH_FILESYSTEM_TEST=1", "DASH_FILESYSTEM_TOOLS="+bundle, binary, "-test.run=^TestFilesystemImages$", "-test.v")
}
