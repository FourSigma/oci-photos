package main

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
)

var config = struct {
	HTTPServerAddress string
	RegistryAddress   string
	OpenAIKey         string
}{
	HTTPServerAddress: cmp.Or(os.Getenv("ADDRESS_HTTP_SERVER"), ":8888"),
	RegistryAddress:   cmp.Or(os.Getenv("ADDRESS_REGISTRY"), "registry:5000"),
	OpenAIKey:         cmp.Or(os.Getenv("OPEN_API_KEY"), "sk-proj-no-key-set-env"),
}

func main() {

	reg, err := remote.NewRegistry(config.RegistryAddress)
	if err != nil {
		slog.Error("failed to init registry client", "err", err)
		return
	}
	reg.PlainHTTP = true
	if err = reg.Ping(context.TODO()); err != nil {
		slog.Error("failed to ping registry", "err", err)
	}

	r := http.NewServeMux()
	r.HandleFunc("/", NotificationHandler)
	r.HandleFunc("/notifications", NotificationHandler)
	r.HandleFunc("/healthz", HealthHandler)

	slog.Info("Starting http server", "port", config.HTTPServerAddress)
	slog.Info("Sever stopped", "err", http.ListenAndServe(config.HTTPServerAddress, r), "port", config.HTTPServerAddress)

}

type Notification struct {
	Events []Event `json:"events"`
}

type Event struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Target    Target `json:"target"`
}

type Target struct {
	MediaType  string `json:"mediaType"`
	Size       int    `json:"size"`
	Digest     string `json:"digest"`
	Repository string `json:"repository"`
	URL        string `json:"url"`
	Tag        string `json:"tag"`
}

func NotificationHandler(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	reg, err := remote.NewRegistry(config.RegistryAddress)
	if err != nil {
		panic(err)
	}
	reg.PlainHTTP = true

	// Read the request body
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Parse the payload JSON
	var payload Notification
	if err := json.Unmarshal(body, &payload); err != nil {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, v := range payload.Events {
		repo, err := reg.Repository(ctx, v.Target.Repository)
		if err != nil {
			slog.Error("error getting repository", "err", err)
			continue
		}

		_, reader, err := repo.FetchReference(ctx, v.Target.Digest)
		if err != nil {
			slog.Error("error fetching reference", "err", err)
			continue
		}

		var manifest v1.Manifest
		if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
			slog.Error("error decoding manifest", "err", err)
			continue
		}

		const NotifcationManifestDescription = "notification.manifest.description"

		if manifest.Annotations == nil {
			manifest.Annotations = map[string]string{}
		}

		if manifest.Annotations[NotifcationManifestDescription] != "true" {
			slog.Info("manifest does not require additional processing")
			continue
		}

		for i, layer := range manifest.Layers {

			rs, err := repo.Fetch(ctx, layer)
			if err != nil {
				slog.Error("error fetching descriptor", "err", err)
				continue
			}

			bs, err := content.ReadAll(rs, layer)
			if err != nil {
				slog.Error("failed to read bytes", "err", err)
				continue
			}

			if layer.Annotations == nil {
				layer.Annotations = map[string]string{}
			}

			slog.Info("post processing image", "size", len(bs))
			description, err := GetDescriptionFromOpenAI(ctx, bs)
			if err != nil {
				slog.Error("failed to ping open-ai", "error", err)
				continue
			}

			layer.Annotations["acme.description.openai"] = description
			slog.Info("finished processing image", "err", err)

			manifest.Layers[i] = layer

		}

		delete(manifest.Annotations, NotifcationManifestDescription)

		mjson, err := json.Marshal(manifest)
		if err != nil {
			slog.Error("error failed to marshal manifest", "err", err)
			continue
		}

		modifiedManifest, err := oras.PushBytes(ctx, repo, manifest.MediaType, mjson)
		if err != nil {
			slog.Error("error failed push final manifest", "err", err)
			continue
		}

		if err := repo.Tag(ctx, modifiedManifest, "latest"); err != nil {
			slog.Error("error failed to tag manifest", "err", err)
			return
		}
	}

	rw.WriteHeader(http.StatusOK)

}

func HealthHandler(rw http.ResponseWriter, r *http.Request) {
	rw.WriteHeader(http.StatusOK)
}

func GetDescriptionFromOpenAI(ctx context.Context, image []byte) (string, error) {

	const template = `
{
  "model": "gpt-4o",
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "Describe this image"
        },
        {
          "type": "image_url",
          "image_url": {
            "url": "data:image/jpeg;base64,%s"
          }
        }
      ]
    }
  ],
  "max_tokens": 300
}`

	payload := fmt.Sprintf(template, base64.StdEncoding.EncodeToString(image))

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to generate new request: %w", err)
	}
	req = req.WithContext(ctx)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", config.OpenAIKey))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed http request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai: failed to get process image - status: %d", resp.StatusCode)
	}

	defer resp.Body.Close()

	bs, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read payload body: %w", err)
	}

	type OpenAIPayload struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	var p OpenAIPayload
	if err := json.Unmarshal(bs, &p); err != nil {
		return "", fmt.Errorf("failed to read payload body: %w", err)
	}

	return p.Choices[0].Message.Content, nil

}
