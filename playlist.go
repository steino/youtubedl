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
	Duration *time.Duration
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

func (p *Playlist) parsePlaylistInfo(ctx context.Context, body []byte) (err error) {
	info, ok := ctx.Value(contextKey("info")).(contextInfo)
	if !ok {
		return errors.New("client is not set")
	}

	var response YouTubeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}

	defer func() {
		stack := debug.Stack()
		if r := recover(); r != nil {
			err = fmt.Errorf("JSON parsing error: %v\n%s", r, stack)
		}
	}()

	playlistMetadata := response.Metadata.PlaylistMetadataRenderer
	if playlistMetadata == nil {
		return fmt.Errorf("playlistMetadataRenderer not found in json body")
	}

	playlistSidebarSecondaryInfoRenderer := response.Sidebar.PlaylistSidebarRenderer.Items[1].PlaylistSidebarSecondaryInfoRenderer
	if playlistSidebarSecondaryInfoRenderer == nil {
		return fmt.Errorf("PlaylistSidebarSecondaryInfoRenderer not found in json body")
	}

	p.Title = playlistMetadata.Title
	p.Description = playlistMetadata.Description
	if len(playlistSidebarSecondaryInfoRenderer.VideoOwner.VideoOwnerRenderer.Title.Runs) > 0 {
		p.Author = playlistSidebarSecondaryInfoRenderer.VideoOwner.VideoOwnerRenderer.Title.Runs[0].Text
	}

	contents := response.Contents
	if contents == nil {
		return fmt.Errorf("contents not found in json body")
	}

	firstPart := contents.TwoColumnBrowseResultsRenderer.Tabs[0].TabRenderer.Content.SectionListRenderer.Contents[0]

	var entries []*PlaylistEntry
	var continuation string

	if firstPart.ItemSectionRenderer.Contents != nil {
		entries, continuation, err = extractPlaylistEntries(*firstPart.ItemSectionRenderer.Contents[0].PlaylistVideoListRenderer.Contents)
		if err != nil {
			return err
		}
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

		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}

		entries, token, err := extractPlaylistEntries(*response.OnResponseReceivedActions[0].AppendContinuationItemsAction.ContinuationItems)
		if err != nil {
			return err
		}

		p.Videos = append(p.Videos, entries...)

		if len(token) > 0 {
			continuation = token
		} else {
			break
		}
	}

	return err
}

func extractPlaylistEntries(vids []PlaylistVideoListContents) ([]*PlaylistEntry, string, error) {
	var continuation string
	entries := make([]*PlaylistEntry, 0, len(vids))

	for _, v := range vids {
		if v.PlaylistVideoRenderer == nil && v.ContinuationItemRenderer != nil {
			continuation = v.ContinuationItemRenderer.ContinuationEndpoint.ContinuationCommand.Token
			continue
		}

		entries = append(entries, v.PlaylistVideoRenderer.PlaylistEntry())
	}

	return entries, continuation, nil
}

func (vje PlaylistVideoRenderer) PlaylistEntry() *PlaylistEntry {
	if vje.LengthSeconds == nil {
		return &PlaylistEntry{
			ID:       vje.VideoID,
			Title:    vje.Title.Runs[0].Text,
			Author:   vje.ShortBylineText.Runs[0].Text,
			Duration: nil,
		}
	}

	val, err := strconv.Atoi(*vje.LengthSeconds)
	if err != nil {
		panic("invalid video duration: " + *vje.LengthSeconds)
	}
	d := time.Second * time.Duration(val)

	return &PlaylistEntry{
		ID:       vje.VideoID,
		Title:    vje.Title.Runs[0].Text,
		Author:   vje.ShortBylineText.Runs[0].Text,
		Duration: &d,
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
