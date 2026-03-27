package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// EnsureQuayRepo creates a quay.io repository if it doesn't already exist.
// Requires QUAY_API_TOKEN env var. The image ref must be in the form
// quay.io[:port]/<namespace>/<repo>:<tag>.
//
// Returns nil if the repo already exists or was created successfully.
func EnsureQuayRepo(imageRef string) error {
	namespace, repo, err := parseQuayImage(imageRef)
	if err != nil {
		return err
	}

	token := quayToken()
	if token == "" {
		return fmt.Errorf("QUAY_API_TOKEN not set; needed to create quay.io repos")
	}

	host := quayHost(imageRef)

	// Check if repo exists
	exists, err := quayRepoExists(host, namespace, repo, token)
	if err != nil {
		return fmt.Errorf("checking repo: %w", err)
	}
	if exists {
		return nil
	}

	// Create it
	return quayCreateRepo(host, namespace, repo, token)
}

// parseQuayImage extracts namespace and repo from a quay.io image reference.
// Handles quay.io/<ns>/<repo>:<tag> and quay.io:443/<ns>/<repo>:<tag>.
func parseQuayImage(imageRef string) (namespace, repo string, err error) {
	// Strip tag
	ref := imageRef
	if idx := strings.LastIndex(ref, ":"); idx > 0 {
		// Be careful: quay.io:443/ns/repo:tag has two colons
		afterColon := ref[idx+1:]
		if !strings.Contains(afterColon, "/") {
			ref = ref[:idx]
		}
	}

	// Strip scheme if present
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimPrefix(ref, "http://")

	// Split on /
	parts := strings.Split(ref, "/")
	// Expected: [quay.io OR quay.io:443, namespace, repo]
	if len(parts) < 3 {
		return "", "", fmt.Errorf("cannot parse quay image ref: %q (expected quay.io/<namespace>/<repo>)", imageRef)
	}

	host := parts[0]
	if !strings.Contains(host, "quay.io") {
		return "", "", fmt.Errorf("not a quay.io image: %q", imageRef)
	}

	return parts[1], parts[2], nil
}

func quayHost(imageRef string) string {
	// quay.io:443/... -> use https://quay.io
	// Always use the API on quay.io port 443
	return "https://quay.io"
}

func quayToken() string {
	return os.Getenv("QUAY_API_TOKEN")
}

func quayRepoExists(host, namespace, repo, token string) (bool, error) {
	url := fmt.Sprintf("%s/api/v1/repository/%s/%s", host, namespace, repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return true, nil
	}
	if resp.StatusCode == 404 {
		return false, nil
	}
	body, _ := io.ReadAll(resp.Body)
	return false, fmt.Errorf("quay API %s: %d %s", url, resp.StatusCode, string(body))
}

func quayCreateRepo(host, namespace, repo, token string) error {
	url := fmt.Sprintf("%s/api/v1/repository", host)

	payload := map[string]any{
		"repository":  repo,
		"namespace":   namespace,
		"visibility":  "public",
		"description": "Created by forge",
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("creating quay repo %s/%s: %w", namespace, repo, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 201 || resp.StatusCode == 200 {
		fmt.Printf("  Created quay.io repo: %s/%s\n", namespace, repo)
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("creating quay repo %s/%s: %d %s", namespace, repo, resp.StatusCode, string(respBody))
}
