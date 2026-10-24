package routes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"charm.land/log/v2"
	"github.com/disintegration/imaging"

	"github.com/damongolding/immich-kiosk/internal/common"
	"github.com/damongolding/immich-kiosk/internal/config"
	"github.com/damongolding/immich-kiosk/internal/kiosk"
	"github.com/damongolding/immich-kiosk/internal/utils"
)

const (
	// nowShowingPreviewSize is the longest side of the preview sent to Home Assistant.
	nowShowingPreviewSize = 640
	// nowShowingStateMax is Home Assistant's limit for entity states.
	nowShowingStateMax = 255
)

// nowShowing is the attributes of the "Image: current" sensor.
type nowShowing struct {
	AssetID     string   `json:"asset_id"`
	Type        string   `json:"type"`
	Filename    string   `json:"filename"`
	Description string   `json:"description,omitempty"`
	Date        string   `json:"date,omitempty"`
	City        string   `json:"city,omitempty"`
	State       string   `json:"state,omitempty"`
	Country     string   `json:"country,omitempty"`
	People      []string `json:"people,omitempty"`
	Album       string   `json:"album,omitempty"`
	Memory      string   `json:"memory,omitempty"`
	Source      string   `json:"source,omitempty"`
	Favorite    bool     `json:"favorite"`
	ImmichURL   string   `json:"immich_url,omitempty"`
	Client      string   `json:"client,omitempty"`
}

// publishNowShowing sends what a client shows to Home Assistant, on the
// client's device and on the all-clients device ("last shown anywhere").
func publishNowShowing(ctx context.Context, baseConfig *config.Config, requestData *common.RouteRequestData, viewData common.ViewData) {
	if controlsMQTT == nil || len(viewData.Assets) == 0 {
		return
	}
	mqttClient := controlsMQTT
	client := controlClient(requestData.ClientName)
	shown := viewData.Assets[0]

	go func() {
		asset := shown.ImmichAsset

		info := nowShowing{
			AssetID:     asset.ID,
			Type:        strings.ToLower(string(asset.Type)),
			Filename:    asset.OriginalFileName,
			Description: strings.TrimSpace(asset.ExifInfo.Description),
			City:        asset.ExifInfo.City,
			State:       asset.ExifInfo.State,
			Country:     asset.ExifInfo.Country,
			Memory:      asset.MemoryTitle,
			Source:      strings.ToLower(string(asset.Bucket)),
			Favorite:    asset.IsFavorite,
			ImmichURL:   immichAssetURL(baseConfig, asset.ID),
			Client:      client,
		}
		if !asset.LocalDateTime.IsZero() {
			// Immich gives the local wall-clock time of the photo marked as UTC.
			info.Date = asset.LocalDateTime.Format("2006-01-02 15:04")
		}
		for _, p := range asset.People {
			if p.Name != "" {
				info.People = append(info.People, p.Name)
			}
		}
		if asset.Bucket == kiosk.SourceAlbum && asset.BucketID != "" {
			info.Album = albumNameResolver(ctx, baseConfig)([]string{asset.BucketID})[0]
		}

		state := info.Description
		if state == "" {
			state = info.Filename
		}
		if r := []rune(strings.Join(strings.Fields(state), " ")); len(r) > nowShowingStateMax {
			state = string(r[:nowShowingStateMax-1]) + "…"
		}

		attributes, err := json.Marshal(info)
		if err != nil {
			log.Error("now showing: marshal attributes", "err", err)
			return
		}
		preview := previewJPEG(shown.ImageData)

		if client != "" {
			mqttClient.PublishNowShowing(client, state, attributes, preview)
		}
		mqttClient.PublishNowShowing("", state, attributes, preview)
	}()
}

// immichAssetURL returns the link to an asset in the Immich web app.
func immichAssetURL(baseConfig *config.Config, assetID string) string {
	base := baseConfig.ImmichExternalURL
	if base == "" {
		base = baseConfig.ImmichURL
	}
	if base == "" || assetID == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/photos/" + assetID
}

// previewJPEG turns a base64 data URI into a small JPEG, or nil on failure.
func previewJPEG(dataURI string) []byte {
	_, data, ok := strings.Cut(dataURI, ";base64,")
	if !ok {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		log.Debug("now showing: decode preview", "err", err)
		return nil
	}
	img, _, err := utils.BytesToImage(raw, false)
	if err != nil {
		log.Debug("now showing: read preview", "err", err)
		return nil
	}

	img = imaging.Fit(img, nowShowingPreviewSize, nowShowingPreviewSize, imaging.Lanczos)
	var buf bytes.Buffer
	if err := imaging.Encode(&buf, img, imaging.JPEG, imaging.JPEGQuality(80)); err != nil {
		log.Debug("now showing: encode preview", "err", err)
		return nil
	}
	return buf.Bytes()
}
