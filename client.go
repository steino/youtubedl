//go:generate /usr/bin/env npm --prefix generatejs/js update
//go:generate go run generatejs/generate.go

package youtubedl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"strconv"
	"sync/atomic"

	"github.com/mengzhuo/cookiestxt"
)

var defaultYoutubeClient = "WEB"
var ErrNoFormat = errors.New("no video format provided")

type Client struct {
	player     *Player
	httpClient *http.Client

	// MaxRoutines to use when downloading a video.
	MaxRoutines int

	// ChunkSize to use when downloading videos in chunks. Default is Size10Mb.
	ChunkSize int64
}

type YoutubeClient struct {
	Name          string `json:"NAME"`
	Version       string `json:"VERSION"`
	UserAgent     string `json:"USER_AGENT,omitempty"`
	DeviceModel   string `json:"DEVICE_MODEL,omitempty"`
	APIKey        string `json:"API_KEY,omitempty"`
	APIVersion    string `json:"API_VERSION,omitempty"`
	StaticVisitor string `json:"STATIC_VISITOR_ID,omitempty"`
	SuggestionExp string `json:"SUGG_EXP_ID,omitempty"`
	SDKVersion    int    `json:"SDK_VERSION,omitempty"`
}

type innertubeRequest struct {
	VideoID         string            `json:"videoId,omitempty"`
	BrowseID        string            `json:"browseId,omitempty"`
	Continuation    string            `json:"continuation,omitempty"`
	Context         inntertubeContext `json:"context"`
	PlaybackContext *playbackContext  `json:"playbackContext,omitempty"`
	ContentCheckOK  bool              `json:"contentCheckOk,omitempty"`
	RacyCheckOk     bool              `json:"racyCheckOk,omitempty"`
	Params          string            `json:"params,omitempty"`
}

type playbackContext struct {
	ContentPlaybackContext contentPlaybackContext `json:"contentPlaybackContext"`
}

type contentPlaybackContext struct {
	SignatureTimestamp int    `json:"signatureTimestamp,omitempty"`
	HTML5Preference    string `json:"html5Preference,omitempty"`
}

type inntertubeContext struct {
	Client innertubeClient `json:"client"`
}

type innertubeClient struct {
	HL                string `json:"hl"`
	GL                string `json:"gl"`
	ClientName        string `json:"clientName"`
	ClientVersion     string `json:"clientVersion"`
	AndroidSDKVersion int    `json:"androidSDKVersion,omitempty"`
	UserAgent         string `json:"userAgent,omitempty"`
	TimeZone          string `json:"timeZone"`
	UTCOffset         int    `json:"utcOffsetMinutes"`
	DeviceModel       string `json:"deviceModel,omitempty"`
	VisitorData       string `json:"visitorData,omitempty"`
}

type clientoptions struct {
	httpClient *http.Client
}

type ClientOpts func(*clientoptions)

func WithHTTPClient(client *http.Client) ClientOpts {
	return func(o *clientoptions) {
		o.httpClient = client
	}
}

func New(opts ...ClientOpts) (out *Client, err error) {
	optsMap := clientoptions{}

	for _, opt := range opts {
		opt(&optsMap)
	}

	if optsMap.httpClient == nil {
		optsMap.httpClient = &http.Client{}
	}

	player, err := NewPlayer()
	if err != nil {
		return
	}

	return &Client{
		player:     player,
		httpClient: optsMap.httpClient,
	}, nil
}

func (c *Client) LoadCookies(path string) (err error) {
	f, err := os.Open("./cookies.txt")
	if err != nil {
		return
	}

	cookies, err := cookiestxt.Parse(f)
	if err != nil {
		return
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatal(err)
	}

	u, err := url.Parse(URLs.YTBase)
	if err != nil {
		log.Fatal(err)
	}

	jar.SetCookies(u, cookies)

	c.httpClient.Jar = jar

	return
}

func (c *Client) GetVideo(id string, opts ...VideoOpts) (*Video, error) {
	return c.GetVideoContext(context.Background(), id, opts...)
}

func (c *Client) GetVideoContext(ctx context.Context, id string, opts ...VideoOpts) (*Video, error) {
	id, err := ExtractVideoID(id)
	if err != nil {
		return nil, fmt.Errorf("extractVideoID failed: %w", err)
	}
	optsMap := videooptions{}

	for _, opt := range opts {
		opt(&optsMap)
	}

	if optsMap.client == "" {
		optsMap.client = defaultYoutubeClient
	}

	client, ok := Clients[optsMap.client]
	if !ok {
		return nil, errors.New("invalid client")
	}
	data := c.player.generatePlayerParams(id, &client)

	uri, err := url.Parse(URLs.YTBase)
	if err != nil {
		return nil, err
	}
	uri.Path = path.Join(uri.Path, "/youtubei/v1/player")

	if client.APIKey != "" {
		query := uri.Query()
		query.Add("key", client.APIKey)
	}

	ctx = context.WithValue(ctx, contextKey("info"), contextInfo{
		Self:   c,
		Client: &client,
		Player: c.player,
	})

	body, err := httpPostBodyBytes(ctx, uri.String(), data)
	if err != nil {
		return nil, err
	}

	v := &Video{
		ID:     id,
		client: &client,
	}

	if err = v.parseVideoInfo(body); err == nil {
		return v, nil
	}

	if errors.Is(err, ErrNotPlayableInEmbed) {
		uri, err := url.Parse(URLs.YTBase)
		if err != nil {
			return nil, err
		}

		uri.Path = path.Join(uri.Path, "/watch")
		query := uri.Query()
		query.Add("v", id)
		query.Add("bpctr", "9999999999")
		query.Add("has_verified", "1")

		html, err := httpGetBodyBytes(ctx, uri.String())
		if err != nil {
			return nil, err
		}

		return v, v.parseVideoPage(html)
	}

	return v, nil
}

// GetPlaylist fetches playlist metadata
func (c *Client) GetPlaylist(url string, opts ...VideoOpts) (*Playlist, error) {
	return c.GetPlaylistContext(context.Background(), url, opts...)
}

// GetPlaylistContext fetches playlist metadata, with a context, along with a list of Videos, and some basic information
// for these videos. Playlist entries cannot be downloaded, as they lack all the required metadata, but
// can be used to enumerate all IDs, Authors, Titles, etc.
func (c *Client) GetPlaylistContext(ctx context.Context, uri string, opts ...VideoOpts) (*Playlist, error) {
	optsMap := videooptions{}

	for _, opt := range opts {
		opt(&optsMap)
	}

	if optsMap.client == "" {
		optsMap.client = defaultYoutubeClient
	}

	client, ok := Clients[optsMap.client]
	if !ok {
		return nil, errors.New("invalid client")
	}

	cinfo := contextInfo{
		Self:   c,
		Player: c.player,
		Client: &client,
	}

	ctx = context.WithValue(ctx, contextKey("info"), cinfo)
	id, err := extractPlaylistID(uri)
	if err != nil {
		return nil, fmt.Errorf("extractPlaylistID failed: %w", err)
	}

	base_uri, err := url.Parse(URLs.YTBase)
	if err != nil {
		return nil, err
	}

	base_uri.Path = path.Join(base_uri.Path, "/youtubei/v1/browse")
	if client.APIKey != "" {
		query := base_uri.Query()
		query.Add("key", client.APIKey)
	}

	data := c.player.prepareInnertubePlaylistData(id, false, &client)
	body, err := httpPostBodyBytes(ctx, base_uri.String(), data)
	if err != nil {
		return nil, err
	}

	p := &Playlist{ID: id}
	return p, p.parsePlaylistInfo(ctx, c, body)
}

func (c *Client) VideoFromPlaylistEntry(entry *PlaylistEntry, opts ...VideoOpts) (*Video, error) {
	return c.GetVideoContext(context.Background(), entry.ID, opts...)
}

func (c *Client) VideoFromPlaylistEntryContext(ctx context.Context, entry *PlaylistEntry, opts ...VideoOpts) (*Video, error) {
	return c.GetVideoContext(ctx, entry.ID, opts...)
}

func (p *Player) generateInnertubeContext(client *YoutubeClient) inntertubeContext {
	return inntertubeContext{
		Client: innertubeClient{
			HL:                "en",
			GL:                "US",
			TimeZone:          "UTC",
			DeviceModel:       client.DeviceModel,
			ClientName:        client.Name,
			ClientVersion:     client.Version,
			AndroidSDKVersion: client.SDKVersion,
			UserAgent:         client.UserAgent,
			VisitorData:       p.visitorData,
		},
	}
}

func (p *Player) prepareInnertubePlaylistData(id string, continuation bool, client *YoutubeClient) innertubeRequest {
	context := p.generateInnertubeContext(client)

	if continuation {
		return innertubeRequest{
			Context:        context,
			Continuation:   id,
			ContentCheckOK: true,
			RacyCheckOk:    true,
		}
	}

	return innertubeRequest{
		Context:        context,
		BrowseID:       "VL" + id,
		ContentCheckOK: true,
		RacyCheckOk:    true,
	}
}

func (p *Player) generatePlayerParams(id string, client *YoutubeClient) innertubeRequest {
	context := p.generateInnertubeContext(client)

	return innertubeRequest{
		VideoID:        id,
		Context:        context,
		ContentCheckOK: true,
		RacyCheckOk:    true,
		// Params:                   playerParams,
		PlaybackContext: &playbackContext{
			ContentPlaybackContext: contentPlaybackContext{
				SignatureTimestamp: p.sig_timestamp,
				// HTML5Preference: "HTML5_PREF_WANTS",
			},
		},
	}
}

func getVisitorData() (out string, err error) {
	uri, err := url.Parse(URLs.YTBase)
	if err != nil {
		return
	}
	uri.Path = path.Join(uri.Path, "/sw.js_data")
	resp, err := http.Get(uri.String())
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	jsonData := body[6:]

	var data []interface{}
	err = json.Unmarshal(jsonData, &data)
	if err != nil {
		return
	}

	if len(data) > 0 {
		if level1, ok := data[0].([]interface{}); ok && len(level1) > 2 {
			if level2, ok := level1[2].([]interface{}); ok && len(level2) > 0 {
				if level3, ok := level2[0].([]interface{}); ok && len(level3) > 0 {
					if level4, ok := level3[0].([]interface{}); ok && len(level4) > 13 {
						if visitorData, ok := level4[13].(string); ok {
							return visitorData, nil
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("visitor data not found in expected structure")
}

func (c *Client) GetStreamURL(video *Video, format *Format) (string, error) {
	return c.GetStreamURLContext(context.Background(), video, format)
}

func (c *Client) GetStreamURLContext(ctx context.Context, video *Video, format *Format) (string, error) {
	if format == nil {
		return "", ErrNoFormat
	}

	return c.player.decipher(format.URL, format.Cipher)
}

func (c *Client) GetStream(video *Video, format *Format) (io.ReadCloser, int64, error) {
	return c.GetStreamContext(context.Background(), video, format)
}

// GetStreamContext returns the stream and the total size for a specific format with a context.
func (c *Client) GetStreamContext(ctx context.Context, video *Video, format *Format) (io.ReadCloser, int64, error) {
	cinfo := contextInfo{
		Self:   c,
		Player: nil,
		Client: video.client,
	}

	ctx = context.WithValue(ctx, contextKey("info"), cinfo)
	url, err := c.GetStreamURL(video, format)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}

	r, w := io.Pipe()
	contentLength := format.ContentLength

	if contentLength == 0 {
		// some videos don't have length information
		contentLength = c.downloadOnce(ctx, req, w, format)
	} else {
		// we have length information, let's download by chunks!
		c.downloadChunked(ctx, req, w, format)
	}

	return r, contentLength, nil
}

func (c *Client) downloadOnce(ctx context.Context, req *http.Request, w *io.PipeWriter, _ *Format) int64 {
	resp, err := httpDo(ctx, req)
	if err != nil {
		w.CloseWithError(err) //nolint:errcheck
		return 0
	}

	go func() {
		defer resp.Body.Close()
		_, err := io.Copy(w, resp.Body)
		if err == nil {
			w.Close()
		} else {
			w.CloseWithError(err) //nolint:errcheck
		}
	}()

	contentLength := resp.Header.Get("Content-Length")
	length, _ := strconv.ParseInt(contentLength, 10, 64)

	return length
}

func (c *Client) downloadChunked(ctx context.Context, req *http.Request, w *io.PipeWriter, format *Format) {
	chunks := getChunks(format.ContentLength, c.getChunkSize())
	maxRoutines := c.getMaxRoutines(len(chunks))

	cancelCtx, cancel := context.WithCancel(ctx)
	abort := func(err error) {
		w.CloseWithError(err)
		cancel()
	}

	currentChunk := atomic.Uint32{}
	for i := 0; i < maxRoutines; i++ {
		go func() {
			for {
				chunkIndex := int(currentChunk.Add(1)) - 1
				if chunkIndex >= len(chunks) {
					// no more chunks
					return
				}

				chunk := &chunks[chunkIndex]
				err := c.downloadChunk(ctx, req.Clone(cancelCtx), chunk)
				close(chunk.data)

				if err != nil {
					abort(err)
					return
				}
			}
		}()
	}

	go func() {
		// copy chunks into the PipeWriter
		for i := 0; i < len(chunks); i++ {
			select {
			case <-cancelCtx.Done():
				abort(context.Canceled)
				return
			case data := <-chunks[i].data:
				_, err := io.Copy(w, bytes.NewBuffer(data))
				if err != nil {
					abort(err)
				}
			}
		}

		// everything succeeded
		w.Close()
	}()
}

func (c *Client) downloadChunk(ctx context.Context, req *http.Request, chunk *chunk) error {
	q := req.URL.Query()
	q.Set("range", fmt.Sprintf("%d-%d", chunk.start, chunk.end))
	req.URL.RawQuery = q.Encode()

	resp, err := httpDo(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ErrUnexpectedStatusCode(resp.StatusCode)
	}

	expected := int(chunk.end-chunk.start) + 1
	data, err := io.ReadAll(resp.Body)
	n := len(data)

	if err != nil {
		return err
	}

	if n != expected {
		return fmt.Errorf("chunk at offset %d has invalid size: expected=%d actual=%d", chunk.start, expected, n)
	}

	chunk.data <- data

	return nil
}

const (
	Size1Kb  = 1024
	Size1Mb  = Size1Kb * 1024
	Size10Mb = Size1Mb * 10
)

func (c *Client) getChunkSize() int64 {
	if c.ChunkSize > 0 {
		return c.ChunkSize
	}

	return Size10Mb
}

func (c *Client) getMaxRoutines(limit int) int {
	routines := 10

	if c.MaxRoutines > 0 {
		routines = c.MaxRoutines
	}

	if limit > 0 && routines > limit {
		routines = limit
	}

	return routines
}
