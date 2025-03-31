package youtubedl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	sjson "github.com/bitly/go-simplejson"
	"golang.org/x/exp/rand"
)

type contextKey string
type contextInfo struct {
	Self        *Client
	Player      *Player
	Client      *YoutubeClient
	VisitorData string
}

func httpPostBodyBytes(ctx context.Context, url string, body interface{}) ([]byte, error) {
	resp, err := httpPost(ctx, url, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func httpPost(ctx context.Context, url string, body interface{}) (*http.Response, error) {
	info, ok := ctx.Value(contextKey("info")).(contextInfo)
	if !ok {
		return nil, errors.New("context values not set")
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Youtube-Client-Name", ClientNameIDs[info.Client.Name])
	req.Header.Set("X-Youtube-Client-Version", info.Client.Version)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := httpDo(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, ErrUnexpectedStatusCode(resp.StatusCode)
	}

	return resp, nil
}

func httpDo(ctx context.Context, req *http.Request) (*http.Response, error) {
	info, ok := ctx.Value(contextKey("info")).(contextInfo)
	if !ok {
		return nil, errors.New("client is not set")
	}

	client := info.Client

	if client.UserAgent != "" {
		req.Header.Set("User-Agent", client.UserAgent)
	}

	req.Header.Set("Origin", "https://youtube.com")
	req.Header.Set("Sec-Fetch-Mode", "navigate")

	consentID := strconv.Itoa(rand.Intn(899) + 100) //nolint:gosec

	req.AddCookie(&http.Cookie{
		Name:   "CONSENT",
		Value:  "YES+cb.20210328-17-p0.en+FX+" + consentID,
		Path:   "/",
		Domain: ".youtube.com",
	})

	res, err := info.Self.httpClient.Do(req)

	log := slog.With("method", req.Method, "url", req.URL)

	if err == nil && res.StatusCode != http.StatusOK {
		err = ErrUnexpectedStatusCode(res.StatusCode)
		res.Body.Close()
		res = nil
	}

	if err != nil {
		log.Debug("HTTP request failed", "error", err)
	} else {
		log.Debug("HTTP request succeeded", "status", res.Status)
	}

	return res, err
}

func httpGetBodyBytes(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpGet(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}
func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpDo(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, ErrUnexpectedStatusCode(resp.StatusCode)
	}

	return resp, nil
}

type chunk struct {
	start int64
	end   int64
	data  chan []byte
}

func getChunks(totalSize, chunkSize int64) []chunk {
	var chunks []chunk

	for start := int64(0); start < totalSize; start += chunkSize {
		end := chunkSize + start - 1
		if end > totalSize-1 {
			end = totalSize - 1
		}

		chunks = append(chunks, chunk{start, end, make(chan []byte, 1)})
	}

	return chunks
}
