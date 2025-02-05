package youtubedl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"runtime/debug"
	"strconv"
	"time"

	sjson "github.com/bitly/go-simplejson"
)

var (
	playlistIDRegex    = regexp.MustCompile("^[A-Za-z0-9_-]{13,42}$")
	playlistInURLRegex = regexp.MustCompile("[&?]list=([A-Za-z0-9_-]{13,42})(&.*)?$")
)

type Playlist struct {
	ID          string
	Title       string
	Description string
	Author      string
	Videos      []*PlaylistEntry
}

type PlaylistEntry struct {
	ID       string
	Title    string
	Author   string
	Duration time.Duration
}

func extractPlaylistID(url string) (string, error) {
	if playlistIDRegex.Match([]byte(url)) {
		return url, nil
	}

	matches := playlistInURLRegex.FindStringSubmatch(url)

	if matches != nil {
		return matches[1], nil
	}

	return "", ErrInvalidPlaylist
}

func (p *Playlist) parsePlaylistInfo(ctx context.Context, client *Client, body []byte) (err error) {
	info, ok := ctx.Value(contextKey("info")).(contextInfo)
	if !ok {
		return errors.New("client is not set")
	}

	var j *sjson.Json
	j, err = sjson.NewJson(body)
	if err != nil {
		return err
	}

	defer func() {
		stack := debug.Stack()
		if r := recover(); r != nil {
			err = fmt.Errorf("JSON parsing error: %v\n%s", r, stack)
		}
	}()

	renderer := j.GetPath("alerts").GetIndex(0).GetPath("alertRenderer")
	if renderer != nil && renderer.GetPath("type").MustString() == "ERROR" {
		message := renderer.GetPath("text", "runs").GetIndex(0).GetPath("text").MustString()

		return ErrPlaylistStatus{Reason: message}
	}

	var metadata *sjson.Json
	if node, ok := j.CheckGet("metadata"); ok {
		metadata = node
	} else if node, ok := j.CheckGet("header"); ok {
		metadata = node
	} else {
		return fmt.Errorf("no playlist header / metadata found")
	}

	metadata = metadata.Get("playlistHeaderRenderer")

	p.Title = sjsonGetText(metadata, "title")
	p.Description = sjsonGetText(metadata, "description", "descriptionText")
	p.Author = j.GetPath("sidebar", "playlistSidebarRenderer", "items").GetIndex(1).
		GetPath("playlistSidebarSecondaryInfoRenderer", "videoOwner", "videoOwnerRenderer", "title", "runs").
		GetIndex(0).Get("text").MustString()

	if len(p.Author) == 0 {
		p.Author = sjsonGetText(metadata, "owner", "ownerText")
	}

	contents, ok := j.CheckGet("contents")
	if !ok {
		return fmt.Errorf("contents not found in json body")
	}

	firstPart := getFirstKeyJSON(contents).GetPath("tabs").GetIndex(0).
		GetPath("tabRenderer", "content", "sectionListRenderer", "contents").GetIndex(0)

	if n := firstPart.GetPath("itemSectionRenderer", "contents").GetIndex(0); isValidJSON(n) {
		firstPart = n
	}

	vJSON, err := firstPart.GetPath("playlistVideoListRenderer", "contents").MarshalJSON()
	if err != nil {
		return err
	}

	if len(vJSON) <= 4 {
		return fmt.Errorf("no video data found in JSON")
	}

	entries, continuation, err := extractPlaylistEntries(vJSON)
	if err != nil {
		return err
	}

	if len(continuation) == 0 {
		continuation = getContinuation(firstPart.Get("playlistVideoListRenderer"))
	}

	if len(entries) == 0 {
		return fmt.Errorf("no videos found in playlist")
	}

	p.Videos = entries

	for continuation != "" {
		data := info.Player.prepareInnertubePlaylistData(continuation, true, info.Client)

		base_uri, err := url.Parse(URLs.YTBase)
		if err != nil {
			return err
		}
		base_uri.Path = path.Join(base_uri.Path, "/youtubei/v1/browse")
		if info.Client.APIKey != "" {
			query := base_uri.Query()
			query.Add("key", info.Client.APIKey)
		}

		body, err := httpPostBodyBytes(ctx, base_uri.String(), data)
		if err != nil {
			return err
		}

		j, err := sjson.NewJson(body)
		if err != nil {
			return err
		}

		next := j.GetPath("onResponseReceivedActions").GetIndex(0).
			GetPath("appendContinuationItemsAction", "continuationItems")

		if !isValidJSON(next) {
			next = j.GetPath("continuationContents", "playlistVideoListContinuation", "contents")
		}

		vJSON, err := next.MarshalJSON()
		if err != nil {
			return err
		}

		entries, token, err := extractPlaylistEntries(vJSON)
		if err != nil {
			return err
		}

		if len(token) > 0 {
			continuation = token
		} else {
			continuation = getContinuation(j.GetPath("continuationContents", "playlistVideoListContinuation"))
		}

		p.Videos = append(p.Videos, entries...)
	}

	return err
}

func extractPlaylistEntries(data []byte) ([]*PlaylistEntry, string, error) {
	var vids []*videosJSONExtractor

	if err := json.Unmarshal(data, &vids); err != nil {
		return nil, "", err
	}

	entries := make([]*PlaylistEntry, 0, len(vids))

	var continuation string
	for _, v := range vids {
		if v.Renderer == nil {
			if v.Continuation.Endpoint.Command.Token != "" {
				continuation = v.Continuation.Endpoint.Command.Token
			}

			continue
		}

		entries = append(entries, v.PlaylistEntry())
	}

	return entries, continuation, nil
}

type videosJSONExtractor struct {
	Renderer *struct {
		ID       string   `json:"videoId"`
		Title    withRuns `json:"title"`
		Author   withRuns `json:"shortBylineText"`
		Duration string   `json:"lengthSeconds"`
	} `json:"playlistVideoRenderer"`
	Continuation struct {
		Endpoint struct {
			Command struct {
				Token string `json:"token"`
			} `json:"continuationCommand"`
		} `json:"continuationEndpoint"`
	} `json:"continuationItemRenderer"`
}

func (vje videosJSONExtractor) PlaylistEntry() *PlaylistEntry {
	ds, err := strconv.Atoi(vje.Renderer.Duration)
	if err != nil {
		panic("invalid video duration: " + vje.Renderer.Duration)
	}
	return &PlaylistEntry{
		ID:       vje.Renderer.ID,
		Title:    vje.Renderer.Title.String(),
		Author:   vje.Renderer.Author.String(),
		Duration: time.Second * time.Duration(ds),
	}
}

type withRuns struct {
	Runs []struct {
		Text string `json:"text"`
	} `json:"runs"`
}

func (wr withRuns) String() string {
	if len(wr.Runs) > 0 {
		return wr.Runs[0].Text
	}
	return ""
}
