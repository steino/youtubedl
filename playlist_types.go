package youtubedl

type textRun struct {
	Text string `json:"text"`
}

type continuationCommand struct {
	Token string `json:"token"`
}

type ContinuationEndpoint struct {
	ContinuationCommand continuationCommand `json:"continuationCommand"`
}

type continuationItemRenderer struct {
	Trigger              string               `json:"trigger"`
	ContinuationEndpoint ContinuationEndpoint `json:"continuationEndpoint"`
}

type PlaylistVideoRenderer struct {
	VideoID string `json:"videoId"`
	Title   struct {
		Runs []textRun `json:"runs"`
	} `json:"title"`
	Index struct {
		SimpleText string `json:"simpleText"`
	} `json:"index"`
	ShortBylineText struct {
		Runs []textRun `json:"runs"`
	} `json:"shortBylineText"`
	LengthSeconds *string `json:"lengthSeconds"`
	VideoInfo     struct {
		Runs []textRun `json:"runs"`
	} `json:"videoInfo"`
}

type PlaylistVideoListContents struct {
	PlaylistVideoRenderer    *PlaylistVideoRenderer    `json:"playlistVideoRenderer"`
	ContinuationItemRenderer *continuationItemRenderer `json:"continuationItemRenderer"`
}

type itemSectionRenderer struct {
	Contents []struct {
		PlaylistVideoListRenderer struct {
			Contents   *[]PlaylistVideoListContents `json:"contents"`
			PlaylistID string                       `json:"playlistId"`
		} `json:"playlistVideoListRenderer"`
	} `json:"contents"`
}

type sectionListRenderer struct {
	Contents []struct {
		ItemSectionRenderer      *itemSectionRenderer      `json:"itemSectionRenderer,omitempty"`
		ContinuationItemRenderer *continuationItemRenderer `json:"continuationItemRenderer,omitempty"`
	} `json:"contents"`
}

type TabRenderer struct {
	Content struct {
		SectionListRenderer sectionListRenderer `json:"sectionListRenderer"`
	} `json:"content"`
}

type twoColumnBrowseResultsRenderer struct {
	Tabs []struct {
		TabRenderer TabRenderer `json:"tabRenderer"`
	} `json:"tabs"`
}

type playlistContents struct {
	TwoColumnBrowseResultsRenderer twoColumnBrowseResultsRenderer `json:"twoColumnBrowseResultsRenderer"`
}

type playlistMetadataRenderer struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type playlistMetadata struct {
	PlaylistMetadataRenderer *playlistMetadataRenderer `json:"playlistMetadataRenderer"`
}

type videoOwnerRenderer struct {
	Title struct {
		Runs []textRun `json:"runs"`
	} `json:"title"`
}

type videoOwner struct {
	VideoOwnerRenderer videoOwnerRenderer `json:"videoOwnerRenderer"`
}

type playlistSidebarSecondaryInfoRenderer struct {
	VideoOwner videoOwner `json:"videoOwner"`
}

type playlistSidebarRenderer struct {
	Items []struct {
		PlaylistSidebarSecondaryInfoRenderer *playlistSidebarSecondaryInfoRenderer `json:"playlistSidebarSecondaryInfoRenderer,omitempty"`
	} `json:"items"`
}

type playlistSidebar struct {
	PlaylistSidebarRenderer playlistSidebarRenderer `json:"playlistSidebarRenderer"`
}

type appendContinuationItemsAction struct {
	ContinuationItems *[]PlaylistVideoListContents `json:"continuationItems"`
}

type responseReceivedAction struct {
	AppendContinuationItemsAction appendContinuationItemsAction `json:"appendContinuationItemsAction"`
}

type YouTubeResponse struct {
	Contents                  *playlistContents        `json:"contents"`
	Metadata                  playlistMetadata         `json:"metadata"`
	OnResponseReceivedActions []responseReceivedAction `json:"onResponseReceivedActions"`
	Sidebar                   playlistSidebar          `json:"sidebar"`
}
