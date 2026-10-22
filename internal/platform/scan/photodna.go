package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // WebP uploads are converted before matching

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// PhotoDNA is the client of the PhotoDNA Cloud Matching API (Match operation):
// the image bytes are POSTed with the subscription key, and the answer's
// IsMatch decides. Every non-OK answer (HTTP status, API status code, a body
// that does not parse) is an error, so the caller rejects the upload and
// stores nothing (fail closed). Info carries the verdict metadata only: what
// moderators see and what a CyberTipline report cites.
type PhotoDNA struct {
	url, key string
	hc       *http.Client
}

// Timeout bounds one Match call (the caller's context may be shorter).
const Timeout = 10 * time.Second

// photoDNAOK is the API status code of a successful call.
const photoDNAOK = 3000

// minSide is PhotoDNA's smallest accepted image side; smaller images are
// scaled up for matching only (the stored variants never are).
const minSide = 160

// NewPhotoDNA returns a client of the Match endpoint url
// (e.g. https://api.microsoftmoderator.com/photodna/v1.0/Match).
func NewPhotoDNA(url, key string) *PhotoDNA {
	return &PhotoDNA{url: url, key: key, hc: &http.Client{Timeout: Timeout}}
}

// WithClient replaces the HTTP client (tests).
func (p *PhotoDNA) WithClient(hc *http.Client) *PhotoDNA {
	p.hc = hc
	return p
}

// Name identifies the driver.
func (p *PhotoDNA) Name() string { return config.ScannerPhotoDNA }

type photoDNAAnswer struct {
	Status struct {
		Code        int    `json:"Code"`
		Description string `json:"Description"`
	} `json:"Status"`
	IsMatch      *bool  `json:"IsMatch"`
	TrackingID   string `json:"TrackingId"`
	MatchDetails struct {
		MatchFlags []struct {
			Source     string   `json:"Source"`
			Violations []string `json:"Violations"`
		} `json:"MatchFlags"`
	} `json:"MatchDetails"`
}

// Scan matches img. JPEG, PNG and GIF of at least 160 px per side are sent as
// they are; anything else (WebP, tiny images) is converted to PNG first.
func (p *PhotoDNA) Scan(ctx context.Context, img []byte, mime string) (Verdict, error) {
	body, ct, err := prepare(img, mime)
	if err != nil {
		return Verdict{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Ocp-Apim-Subscription-Key", p.key)
	res, err := p.hc.Do(req) //nolint:gosec // operator-configured PhotoDNA endpoint
	if err != nil {
		return Verdict{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return Verdict{}, fmt.Errorf("scan: photodna: http %d", res.StatusCode)
	}
	var a photoDNAAnswer
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&a); err != nil {
		return Verdict{}, fmt.Errorf("scan: photodna: %w", err)
	}
	if a.Status.Code != photoDNAOK || a.IsMatch == nil {
		return Verdict{}, fmt.Errorf("scan: photodna: status %d", a.Status.Code)
	}
	if !*a.IsMatch {
		return Verdict{}, nil
	}
	info := map[string]string{"driver": config.ScannerPhotoDNA, "tracking_id": a.TrackingID, "status": strconv.Itoa(a.Status.Code)}
	var sources, violations []string
	for _, f := range a.MatchDetails.MatchFlags {
		if f.Source != "" {
			sources = append(sources, f.Source)
		}
		violations = append(violations, f.Violations...)
	}
	if len(sources) > 0 {
		info["sources"] = strings.Join(sources, ",")
	}
	if len(violations) > 0 {
		info["violations"] = strings.Join(violations, ",")
	}
	return Verdict{Match: true, Info: info}, nil
}

var errUnscannable = errors.New("scan: image cannot be prepared for matching")

// prepare returns the bytes and content type sent to the matcher.
func prepare(img []byte, mime string) ([]byte, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		return nil, "", errUnscannable
	}
	direct := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif"}
	if ct, ok := direct[format]; ok && cfg.Width >= minSide && cfg.Height >= minSide && (mime == "" || mime == ct) {
		return img, ct, nil
	}
	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, "", errUnscannable
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < minSide || h < minSide {
		scale := float64(minSide) / float64(min(w, h))
		w, h = int(float64(w)*scale+0.5), int(float64(h)*scale+0.5)
		dst := image.NewNRGBA(image.Rect(0, 0, max(w, minSide), max(h, minSide)))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
		src = dst
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		return nil, "", errUnscannable
	}
	return buf.Bytes(), "image/png", nil
}
