package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// hubAPI is Docker Hub's API root; tests point it at a local server.
var hubAPI = "https://hub.docker.com"

const (
	// hubPageSize and hubMaxPages cap a tag listing at 300 tags. A missing
	// tag's nearest neighbour is almost always among the recent ones.
	hubPageSize = 100
	hubMaxPages = 3
	// hubMaxBody caps one page of the response.
	hubMaxBody = 4 << 20
)

var errHubNotFound = errors.New("docker hub: repository not found or not public")

func (d dockerLocal) HubTags(ctx context.Context, repository, contains string) ([]string, error) {
	ns, name, ok := strings.Cut(repository, "/")
	if !ok || ns == "" || name == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf("docker hub: %q is not namespace/name", repository)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c := &http.Client{}
	next := fmt.Sprintf("%s/v2/namespaces/%s/repositories/%s/tags?page_size=%d&ordering=last_updated",
		hubAPI, url.PathEscape(ns), url.PathEscape(name), hubPageSize)
	if contains != "" {
		next += "&name=" + url.QueryEscape(contains)
	}
	var tags []string
	for page := 0; page < hubMaxPages && next != ""; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("docker hub: %w", err)
		}
		var body struct {
			Next    string `json:"next"`
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		}
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized:
			err = errHubNotFound
		case resp.StatusCode != http.StatusOK:
			err = fmt.Errorf("docker hub: status %d", resp.StatusCode)
		default:
			err = json.NewDecoder(io.LimitReader(resp.Body, hubMaxBody)).Decode(&body)
		}
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, r := range body.Results {
			tags = append(tags, r.Name)
		}
		// Follow only links that stay on the API host.
		next = ""
		if strings.HasPrefix(body.Next, hubAPI+"/") {
			next = body.Next
		}
	}
	return tags, nil
}
