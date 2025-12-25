package videoinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/google/uuid"
	"github.com/krelinga/go-libs/deep"
	"github.com/krelinga/video-info/virest"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestTranscodeEndToEnd(t *testing.T) {
	ctx := context.Background()

	// Create temp directory for media files
	tempDir, err := os.MkdirTemp("", "info-e2e-*")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}

	// Copy test file to temp directory
	srcFile := "testdata/testdata_sample_640x360.mkv"
	dstFile := filepath.Join(tempDir, "testdata_sample_640x360.mkv")
	if err := copyFile(srcFile, dstFile); err != nil {
		t.Fatalf("failed to copy test file: %v", err)
	}

	// Create Docker network
	net, err := network.New(ctx, network.WithCheckDuplicate())
	if err != nil {
		t.Fatalf("failed to create network: %v", err)
	}
	networkName := net.Name

	// Database configuration
	dbName := "videotranscoder"
	dbUser := "postgres"
	dbPassword := "postgres"

	// Start postgres container
	postgresReq := testcontainers.ContainerRequest{
		Image:        "postgres:16",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       dbName,
			"POSTGRES_USER":     dbUser,
			"POSTGRES_PASSWORD": dbPassword,
		},
		Networks:       []string{networkName},
		NetworkAliases: map[string][]string{networkName: {"postgres"}},
		WaitingFor:     wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
	}
	postgresContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: postgresReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	t.Cleanup(func() {
		dumpContainerLogs(t, ctx, postgresContainer, "postgres")
	})

	// Start MockServer container for webhook testing
	mockServerReq := testcontainers.ContainerRequest{
		Image:          "mockserver/mockserver:5.15.0",
		ExposedPorts:   []string{"1080/tcp"},
		Networks:       []string{networkName},
		NetworkAliases: map[string][]string{networkName: {"mockserver"}},
		WaitingFor:     wait.ForLog("started on port: 1080"),
	}
	mockServerContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: mockServerReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start mockserver container: %v", err)
	}
	t.Cleanup(func() {
		dumpContainerLogs(t, ctx, mockServerContainer, "mockserver")
	})

	// Get MockServer mapped port for verification calls
	mockServerPort, err := mockServerContainer.MappedPort(ctx, "1080")
	if err != nil {
		t.Fatalf("failed to get mockserver mapped port: %v", err)
	}
	mockServerHost, err := mockServerContainer.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get mockserver host: %v", err)
	}
	mockServerURL := fmt.Sprintf("http://%s:%s", mockServerHost, mockServerPort.Port())

	// Set up MockServer expectation for webhook endpoint
	setupMockServerExpectation(t, mockServerURL, "/webhook")

	// Common environment variables for server and worker
	dbEnv := map[string]string{
		"VI_DB_HOST":     "postgres",
		"VI_DB_PORT":     "5432",
		"VI_DB_USER":     dbUser,
		"VI_DB_PASSWORD": dbPassword,
		"VI_DB_NAME":     dbName,
		"VI_SERVER_PORT": "8080",
	}

	// Build and start server container
	serverReq := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    ".",
			Dockerfile: "Dockerfile",
			BuildArgs:  map[string]*string{},
			BuildOptionsModifier: func(buildOptions *build.ImageBuildOptions) {
				buildOptions.Target = "server"
			},
		},
		ExposedPorts:   []string{"8080/tcp"},
		Env:            dbEnv,
		Networks:       []string{networkName},
		NetworkAliases: map[string][]string{networkName: {"server"}},
		WaitingFor:     wait.ForLog("Starting HTTP server on port 8080"),
	}
	serverContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: serverReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start server container: %v", err)
	}
	t.Cleanup(func() {
		dumpContainerLogs(t, ctx, serverContainer, "server")
	})

	// Build and start worker container with volume mount
	workerReq := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    ".",
			Dockerfile: "Dockerfile",
			BuildArgs:  map[string]*string{},
			BuildOptionsModifier: func(buildOptions *build.ImageBuildOptions) {
				buildOptions.Target = "worker"
			},
		},
		Env:            dbEnv,
		Networks:       []string{networkName},
		NetworkAliases: map[string][]string{networkName: {"worker"}},
		Mounts: testcontainers.Mounts(
			testcontainers.BindMount(tempDir, "/nas/media"),
		),
		WaitingFor: wait.ForLog("Worker started, waiting for jobs..."),
	}
	workerContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: workerReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start worker container: %v", err)
	}
	t.Cleanup(func() {
		dumpContainerLogs(t, ctx, workerContainer, "worker")
	})

	// Get server mapped port
	mappedPort, err := serverContainer.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("failed to get server mapped port: %v", err)
	}
	serverHost, err := serverContainer.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get server host: %v", err)
	}
	serverURL := fmt.Sprintf("http://%s:%s", serverHost, mappedPort.Port())

	// Create virest client
	client, err := virest.NewClientWithResponses(serverURL)
	if err != nil {
		t.Fatalf("failed to create virest client: %v", err)
	}

	// Create info job with webhook
	jobUUID := uuid.New()
	sourcePath := "/nas/media/testdata_sample_640x360.mkv"
	webhookURI := "http://mockserver:1080/webhook"
	webhookToken := []byte("test-webhook-token")

	createResp, err := client.CreateInfoWithResponse(ctx, virest.CreateInfoJSONRequestBody{
		Uuid:         jobUUID,
		VideoPath:    sourcePath,
		WebhookUri:   &webhookURI,
		WebhookToken: webhookToken,
	})
	if err != nil {
		t.Fatalf("failed to create info job: %v", err)
	}
	if createResp.JSON201 == nil {
		t.Fatalf("expected 201 response, got status %d: %s", createResp.StatusCode(), string(createResp.Body))
	}

	t.Logf("Created info job with UUID: %s", jobUUID)

	// Poll for job completion
	var finalJob virest.InfoJob
	for {
		statusResp, err := client.GetInfoStatusWithResponse(ctx, jobUUID)
		if err != nil {
			t.Fatalf("failed to get info status: %v %v", err, statusResp)
		}
		if statusResp.JSON200 == nil {
			t.Fatalf("expected 200 response, got status %d: %s", statusResp.StatusCode(), string(statusResp.Body))
		}

		job := statusResp.JSON200

		if job.Status == virest.Completed || job.Status == virest.Failed {
			finalJob = *job
			if job.Error != nil {
				t.Logf("Job error: %s", *job.Error)
			}
			break
		}

		time.Sleep(2 * time.Second)
	}

	// Verify job completed successfully
	if finalJob.Status != virest.Completed {
		t.Fatalf("expected job to complete successfully, but got status: %s", finalJob.Status)
	}

	// TODO: Verify extracted info fields
	t.Log(deep.Format(deep.NewEnv(), finalJob))

	t.Logf("Info job completed successfully with UUID: %s", jobUUID)

	// Verify webhook was received
	webhookPayload := waitForWebhook(t, ctx, mockServerURL, "/webhook", 30*time.Second)
	if webhookPayload == nil {
		t.Fatalf("webhook was not received within timeout")
	}

	// Verify webhook payload contents
	if webhookPayload.Uuid != jobUUID {
		t.Errorf("webhook UUID mismatch: got %s, want %s", webhookPayload.Uuid, jobUUID)
	}
	if !bytes.Equal(webhookPayload.Token, webhookToken) {
		t.Errorf("webhook token mismatch: got %v, want %v", webhookPayload.Token, webhookToken)
	}
	if webhookPayload.Result == nil {
		t.Errorf("webhook result should not be nil for successful job")
	}
	if webhookPayload.Error != nil {
		t.Errorf("webhook error should be nil for successful job, got: %s", *webhookPayload.Error)
	}
	t.Logf("Webhook received successfully: %s", deep.Format(deep.NewEnv(), webhookPayload))

	// Test duplicate UUID rejection - try to create another job with same UUID but different destination
	duplicateResp, err := client.CreateInfoWithResponse(ctx, virest.CreateInfoJSONRequestBody{
		Uuid:      jobUUID,
		VideoPath: sourcePath,
	})
	if err != nil {
		t.Fatalf("failed to send duplicate info request: %v", err)
	}
	if duplicateResp.StatusCode() != 409 {
		t.Fatalf("expected 409 response for duplicate UUID, got status %d: %s", duplicateResp.StatusCode(), string(duplicateResp.Body))
	}
	if duplicateResp.JSON409 == nil {
		t.Fatalf("expected JSON409 response body for duplicate UUID")
	}
	t.Logf("Duplicate UUID correctly rejected with 409: %s", duplicateResp.JSON409.Message)

}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// dumpContainerLogs reads and logs all output from a container
func dumpContainerLogs(t *testing.T, ctx context.Context, container testcontainers.Container, name string) {
	logs, err := container.Logs(ctx)
	if err != nil {
		t.Logf("failed to get %s container logs: %v", name, err)
		return
	}
	defer logs.Close()

	logBytes, err := io.ReadAll(logs)
	if err != nil {
		t.Logf("failed to read %s container logs: %v", name, err)
		return
	}

	t.Logf("=== %s container logs ===\n%s", name, string(logBytes))
}

// WebhookPayload matches the structure sent by the webhook worker
type WebhookPayload struct {
	Token  []byte            `json:"token,omitempty"`
	Uuid   uuid.UUID         `json:"uuid"`
	Result *virest.VideoInfo `json:"result,omitempty"`
	Error  *string           `json:"error,omitempty"`
}

// setupMockServerExpectation configures MockServer to accept POST requests
func setupMockServerExpectation(t *testing.T, mockServerURL, path string) {
	expectation := map[string]interface{}{
		"httpRequest": map[string]interface{}{
			"method": "POST",
			"path":   path,
		},
		"httpResponse": map[string]interface{}{
			"statusCode": 200,
		},
	}

	body, _ := json.Marshal(expectation)
	req, err := http.NewRequest(http.MethodPut, mockServerURL+"/mockserver/expectation", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create mockserver expectation request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to set up mockserver expectation: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("failed to set up mockserver expectation, status %d: %s", resp.StatusCode, respBody)
	}
}

// waitForWebhook polls MockServer for received requests until one is found or timeout
func waitForWebhook(t *testing.T, ctx context.Context, mockServerURL, path string, timeout time.Duration) *WebhookPayload {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		payload, found := checkForWebhook(t, mockServerURL, path)
		if found {
			return payload
		}
		time.Sleep(500 * time.Millisecond)
	}

	return nil
}

// checkForWebhook queries MockServer for recorded requests
func checkForWebhook(t *testing.T, mockServerURL, path string) (*WebhookPayload, bool) {
	reqBody := map[string]interface{}{
		"path": path,
	}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest(http.MethodPut, mockServerURL+"/mockserver/retrieve?type=REQUESTS", bytes.NewReader(body))
	if err != nil {
		t.Logf("failed to create retrieve request: %v", err)
		return nil, false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("failed to retrieve mockserver requests: %v", err)
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Logf("mockserver retrieve returned status %d: %s", resp.StatusCode, respBody)
		return nil, false
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}

	// MockServer returns an array of recorded requests with nested body structure
	var requests []struct {
		Body struct {
			Type   string          `json:"type"`
			Json   json.RawMessage `json:"json"`
			String string          `json:"string"`
		} `json:"body"`
	}

	if err := json.Unmarshal(respBody, &requests); err != nil {
		t.Logf("failed to parse mockserver response: %v, body: %s", err, respBody)
		return nil, false
	}

	if len(requests) == 0 {
		return nil, false
	}

	// Try parsing from json field first, then string field
	var payload WebhookPayload
	bodyData := requests[0].Body.Json
	if len(bodyData) == 0 && requests[0].Body.String != "" {
		bodyData = []byte(requests[0].Body.String)
	}

	if len(bodyData) == 0 {
		t.Logf("no body data found in request")
		return nil, false
	}

	if err := json.Unmarshal(bodyData, &payload); err != nil {
		t.Logf("failed to parse webhook payload: %v, body: %s", err, bodyData)
		return nil, false
	}

	return &payload, true
}
