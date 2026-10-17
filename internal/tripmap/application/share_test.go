package application_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/tripmap/app"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/mediaobject"
	entshare "github.com/kongken/mono/internal/tripmap/ent/share"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/kongken/mono/pkg/proto/tripmap/v1/tripmapv1connect"
)

const (
	shareBase    = "https://trip.test"
	secretNote   = "秘密笔记XYZ"
	secretCity   = "杭州市"
	indexTitle   = "<title>原始标题</title>"
	indexHTMLFmt = `<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8" />` + indexTitle + `<meta name="description" content="旧描述" /><meta property="og:title" content="旧标题" /></head><body><div id="root"></div><script type="module" src="/assets/index.js"></script></body></html>`
)

// shareEnv runs the real routes (Gin, interceptor, share handlers) over an
// isolated PostgreSQL schema and Redis prefix, with an in-memory object store.
type shareEnv struct {
	*env
	mem       *mediaMemory
	server    *httptest.Server
	share     tripmapv1connect.ShareServiceClient
	index     *httptest.Server
	indexHits atomic.Int64
	indexCode atomic.Int64
	handlers  *app.Handlers
	resources *app.Resources
}

func newShareEnv(t *testing.T, mutate ...func(*config.AppConfig)) *shareEnv {
	t.Helper()
	se := &shareEnv{}
	se.indexCode.Store(http.StatusOK)
	se.index = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		se.indexHits.Add(1)
		if r.Header.Get("X-Tripmap-Index-Fetch") == "" {
			t.Error("index fetch is not marked as the backend's own request")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(int(se.indexCode.Load()))
		_, _ = io.WriteString(w, indexHTMLFmt)
	}))
	t.Cleanup(se.index.Close)
	mutators := append([]func(*config.AppConfig){func(c *config.AppConfig) {
		c.Share = config.ShareConfig{PublicBaseURL: shareBase, AppIndexURL: se.index.URL + "/"}
	}}, mutate...)
	se.env = newEnv(t, mutators...)
	se.mem = &mediaMemory{files: map[string][]byte{}}
	se.store.Blobs = se.mem
	rdb, ok := se.store.Redis.(*redis.Client)
	if !ok {
		t.Fatal("test Redis is not a client")
	}
	se.resources = &app.Resources{DB: se.store.DB, Redis: rdb, Blobs: se.mem, KeyPrefix: se.store.KeyPrefix,
		Health: health.New(health.Probe{Name: "test", Check: func(context.Context) error { return nil }})}
	se.handlers = app.New(se.cfg)
	se.handlers.SetResources(se.resources)
	gin.SetMode(gin.TestMode)
	index, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	opts := connect.WithInterceptors(application.NewAuthInterceptor(se.handlers))
	api := router.Group("/api", httpx.CORS(func() []string { return se.cfg.Auth.CORSAllowedOrigins }, "X-Tripmap-Schema", "X-Tripmap-Regions"))
	path, handler := tripmapv1connect.NewShareServiceHandler(application.NewShareService(se.cfg, se.handlers, index), opts)
	api.Any(path+"*path", gin.WrapH(http.StripPrefix("/api", handler)))
	app.MountShare(router, api, application.NewSharePublic(se.cfg, se.handlers))
	se.server = httptest.NewServer(router)
	t.Cleanup(se.server.Close)
	se.share = tripmapv1connect.NewShareServiceClient(se.server.Client(), se.server.URL+"/api")
	return se
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (se *shareEnv) get(path string, headers ...string) response {
	se.t.Helper()
	req, err := http.NewRequest(http.MethodGet, se.server.URL+path, nil)
	if err != nil {
		se.t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := se.server.Client().Do(req)
	if err != nil {
		se.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return response{res.StatusCode, res.Header, body}
}

func tokenOf(url string) string { return strings.TrimPrefix(url, shareBase+"/s/") }

// footprint seeds Hangzhou with a dated, annotated trip and a marked province.
func (se *shareEnv) footprint(token string) int64 {
	se.t.Helper()
	rev := se.mustSync2(token, "seed-"+uuid.NewString(), 0,
		upCity("330100", secretCity, "330000", "#4b937d"), upTrip("330100", "trip1", "2025-05-01", secretNote),
		&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: &tripmapv1.ProvinceMark{Province: "530000", Color: "#e2af5c"}}})
	return rev
}

func settings(visibility string, cities, dates, notes, photos bool, ids ...string) *tripmapv1.ShareSettings {
	return &tripmapv1.ShareSettings{Visibility: visibility, ShowCities: cities, ShowDates: dates, ShowNotes: notes, ShowPhotos: photos, PhotoIds: ids}
}

func (se *shareEnv) preview(token string, s *tripmapv1.ShareSettings) *tripmapv1.PreviewShareResponse {
	se.t.Helper()
	res, err := se.share.PreviewShare(context.Background(), bearer(token, &tripmapv1.PreviewShareRequest{Settings: s}))
	if err != nil {
		se.t.Fatalf("preview: %v", err)
	}
	return res.Msg
}

func (se *shareEnv) get1(token string) *tripmapv1.ShareInfo {
	se.t.Helper()
	res, err := se.share.GetShare(context.Background(), bearer(token, &tripmapv1.GetShareRequest{}))
	if err != nil {
		se.t.Fatal(err)
	}
	return res.Msg.Share
}

func (se *shareEnv) save(token string, version int64, s *tripmapv1.ShareSettings, refresh bool, previewRevision int64) (*tripmapv1.SaveShareResponse, error) {
	res, err := se.share.SaveShare(context.Background(), bearer(token, &tripmapv1.SaveShareRequest{
		MutationId: uuid.NewString(), ExpectedVersion: version, Settings: s, RefreshContent: refresh, PreviewRevision: previewRevision}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// publish previews and then saves with the previewed revision, like the app.
func (se *shareEnv) publish(token string, s *tripmapv1.ShareSettings) *tripmapv1.ShareInfo {
	se.t.Helper()
	version := se.get1(token).Version
	p := se.preview(token, s)
	res, err := se.save(token, version, s, true, p.FootprintRevision)
	if err != nil {
		se.t.Fatalf("publish: %v", err)
	}
	return res.Share
}

func (se *shareEnv) uploadPhoto(token, tripID string) string {
	se.t.Helper()
	ctx := context.Background()
	data := photo(se.t)
	r := preparePhoto(data)
	r.TripId = tripID
	res, err := se.media.PrepareUpload(ctx, bearer(token, r))
	if err != nil {
		se.t.Fatal(err)
	}
	id := res.Msg.Media.Id
	m := se.store.DB.MediaObject.GetX(ctx, id)
	if err = se.mem.Put(ctx, m.UploadKey, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		se.t.Fatal(err)
	}
	if _, err = se.media.CompleteUpload(ctx, bearer(token, &tripmapv1.CompleteUploadRequest{Id: id})); err != nil {
		se.t.Fatal(err)
	}
	return id
}

type publicDoc struct {
	Version     int      `json:"v"`
	PublishedOn string   `json:"published_on"`
	Provinces   []string `json:"provinces"`
	Fields      struct {
		Cities, Dates, Notes, Photos bool
	} `json:"fields"`
	Stats struct {
		ExploredProvinces int `json:"explored_provinces"`
		Cities            int `json:"cities"`
		Trips             int `json:"trips"`
		TotalCities       int `json:"total_cities"`
	} `json:"stats"`
	Cities []struct {
		Code  string `json:"code"`
		Name  string `json:"name"`
		Trips []struct {
			Date, Note string
			Photos     []struct {
				ID string `json:"id"`
			}
		} `json:"trips"`
		Photos []struct {
			ID string `json:"id"`
		} `json:"photos"`
	} `json:"cities"`
}

func (se *shareEnv) data(tok string) (publicDoc, response) {
	se.t.Helper()
	res := se.get("/api/share/" + tok + "/data")
	var doc publicDoc
	if res.status == http.StatusOK {
		if err := json.Unmarshal(res.body, &doc); err != nil {
			se.t.Fatalf("data is not JSON: %v: %s", err, res.body)
		}
	}
	return doc, res
}

// identifiers a public response must never contain.
func (se *shareEnv) secrets(username string, extra ...string) []string {
	u := se.store.DB.User.Query().Where(user.UsernameEQ(username)).OnlyX(context.Background())
	out := []string{u.ID, username, "trip1", "legacy-", "media/files", "media/thumbnails", "西湖", "user_id", "userId", "revision", "tm_"}
	return append(out, extra...)
}

func assertNoSecrets(t *testing.T, label string, body []byte, secrets []string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(string(body), s) {
			t.Errorf("%s leaks %q: %.300s", label, s, body)
		}
	}
}

// uniform asserts that a response is the one 404 every failed lookup returns.
func uniform(t *testing.T, label string, res response, want response) {
	t.Helper()
	if res.status != http.StatusNotFound || !bytes.Equal(res.body, want.body) {
		t.Fatalf("%s: status %d body %.80q", label, res.status, res.body)
	}
	for _, h := range []string{"Content-Type", "Cache-Control", "X-Robots-Tag", "Referrer-Policy", "Content-Security-Policy", "X-Content-Type-Options"} {
		if res.header.Get(h) != want.header.Get(h) {
			t.Fatalf("%s: header %s = %q, want %q", label, h, res.header.Get(h), want.header.Get(h))
		}
	}
	if res.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%s: 404 is cacheable: %q", label, res.header.Get("Cache-Control"))
	}
}

func TestShareDefaultsPublishOnlyProvinceLevelAggregates(t *testing.T) {
	se := newShareEnv(t)
	tok := se.register("share_defaults")
	se.footprint(tok)
	if info := se.get1(tok); info.Settings.Visibility != "private" || info.Url != "" || info.Version != 0 {
		t.Fatalf("initial share %v", info)
	}
	info := se.publish(tok, settings("link", false, false, false, false))
	if !strings.HasPrefix(info.Url, shareBase+"/s/") || len(tokenOf(info.Url)) != 43 || !info.Published || info.Version != 1 {
		t.Fatalf("share %v", info)
	}
	doc, res := se.data(tokenOf(info.Url))
	if res.status != http.StatusOK {
		t.Fatalf("data %d", res.status)
	}
	if doc.Stats.ExploredProvinces != 2 || doc.Stats.Cities != 1 || doc.Stats.Trips != 1 || doc.Stats.TotalCities != 391 ||
		strings.Join(doc.Provinces, ",") != "330000,530000" || len(doc.Cities) != 0 || doc.Fields.Cities {
		t.Fatalf("doc %+v", doc)
	}
	assertNoSecrets(t, "data", res.body, se.secrets("share_defaults", secretCity, secretNote, "2025-05-01", "330100", "#4b937d"))
	for name, want := range map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff", "X-Robots-Tag": "noindex, nofollow"} {
		if got := res.header.Get(name); got != want {
			t.Errorf("data header %s = %q, want %q", name, got, want)
		}
	}
	page := se.get("/s/" + tokenOf(info.Url))
	if page.status != 200 {
		t.Fatalf("page %d", page.status)
	}
	assertNoSecrets(t, "page", page.body, se.secrets("share_defaults", secretCity, secretNote, "2025-05-01"))
}

func TestShareDependencyRulesAndAuthentication(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	tok := se.register("share_rules")
	for name, s := range map[string]*tripmapv1.ShareSettings{
		"dates without cities":  settings("link", false, true, false, false),
		"notes without cities":  settings("link", false, false, true, false),
		"photos without cities": settings("link", false, false, false, true),
		"photo ids sans photos": settings("link", true, false, false, false, "x"),
		"unknown visibility":    settings("everyone", true, false, false, false),
		"duplicate photo":       settings("link", true, false, false, true, "x", "x"),
	} {
		_, err := se.share.PreviewShare(ctx, bearer(tok, &tripmapv1.PreviewShareRequest{Settings: s}))
		if name != "unknown visibility" {
			wantCode(t, err, connect.CodeInvalidArgument)
		}
		_, err = se.save(tok, 0, s, true, 0)
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	var ids []string
	for i := 0; i < 13; i++ {
		ids = append(ids, uuid.NewString())
	}
	_, err := se.save(tok, 0, settings("link", true, false, false, true, ids...), true, 0)
	wantCode(t, err, connect.CodeInvalidArgument)
	// Nothing without a session, and the account is never a request field.
	_, err = se.share.GetShare(ctx, bearer("", &tripmapv1.GetShareRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = se.share.SaveShare(ctx, bearer("tm_nope", &tripmapv1.SaveShareRequest{MutationId: "m1", Settings: settings("link", false, false, false, false)}))
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = se.share.RevokeShare(ctx, bearer("", &tripmapv1.RevokeShareRequest{MutationId: "m1"}))
	wantCode(t, err, connect.CodeUnauthenticated)
	// Publishing first content needs refresh_content and a preview revision.
	_, err = se.save(tok, 0, settings("link", false, false, false, false), false, 0)
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = se.save(tok, 0, settings("private", false, false, false, false), true, 0)
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestShareUsersAreIsolated(t *testing.T) {
	se := newShareEnv(t)
	a, b := se.register("share_iso_a"), se.register("share_iso_b")
	se.footprint(a)
	infoA := se.publish(a, settings("link", true, true, true, false))
	if got := se.get1(b); got.Url != "" || got.Version != 0 {
		t.Fatalf("user B sees A's share: %v", got)
	}
	// B revoking or saving never touches A's link.
	if _, err := se.share.RevokeShare(context.Background(), bearer(b, &tripmapv1.RevokeShareRequest{MutationId: "b-revoke"})); err != nil {
		t.Fatal(err)
	}
	if _, res := se.data(tokenOf(infoA.Url)); res.status != 200 {
		t.Fatalf("A's page vanished: %d", res.status)
	}
	infoB := se.publish(b, settings("link", false, false, false, false))
	if tokenOf(infoA.Url) == tokenOf(infoB.Url) {
		t.Fatal("two users share a token")
	}
	docB, _ := se.data(tokenOf(infoB.Url))
	if docB.Stats.Cities != 0 || len(docB.Cities) != 0 {
		t.Fatalf("B's page shows A's footprints: %+v", docB)
	}
}

func TestShareContentIsAnExplicitSnapshotAndRefreshNeedsConfirmation(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	tok := se.register("share_snapshot")
	rev := se.footprint(tok)
	info := se.publish(tok, settings("link", true, true, true, false))
	id := tokenOf(info.Url)
	doc, res := se.data(id)
	if len(doc.Cities) != 1 || doc.Cities[0].Name != secretCity || doc.Cities[0].Trips[0].Note != secretNote || doc.Cities[0].Trips[0].Date != "2025-05-01" {
		t.Fatalf("doc %+v", doc)
	}
	assertNoSecrets(t, "data", res.body, se.secrets("share_snapshot"))
	// New cities, trips and notes stay private until the owner publishes again.
	rev = se.mustSync2(tok, "later", rev, upCity("350200", "厦门市", "350000", "#e2af5c"), upTrip("350200", "trip2", "2026-01-01", "新笔记"),
		upTrip("330100", "trip1", "2025-05-01", "改过的笔记"))
	doc, res = se.data(id)
	if len(doc.Cities) != 1 || strings.Contains(string(res.body), "新笔记") || strings.Contains(string(res.body), "厦门") || strings.Contains(string(res.body), "改过") {
		t.Fatalf("a later footprint reached the page: %s", res.body)
	}
	state := se.get1(tok)
	if state.PublishedRevision == state.CurrentRevision || state.CurrentRevision != rev {
		t.Fatalf("staleness not reported: %v", state)
	}
	// The previewed revision is bound to the publish: a stale one is aborted, not published.
	stale := se.preview(tok, settings("link", true, true, true, false))
	rev = se.mustSync2(tok, "after-preview", rev, upTrip("330100", "trip3", "2026-02-02", "预览后新增的备注"))
	_, err := se.save(tok, state.Version, settings("link", true, true, true, false), true, stale.FootprintRevision)
	wantCode(t, err, connect.CodeAborted)
	if !strings.Contains(string(se.get("/api/share/"+id+"/data").body), "秘密笔记XYZ") {
		t.Fatal("the aborted publish changed the page")
	}
	// A fresh preview shows exactly what is then published.
	fresh := se.preview(tok, settings("link", true, true, true, false))
	if fresh.FootprintRevision != rev || !strings.Contains(fresh.ContentJson, "预览后新增的备注") {
		t.Fatalf("preview %v", fresh)
	}
	res2, err := se.save(tok, state.Version, settings("link", true, true, true, false), true, fresh.FootprintRevision)
	if err != nil || res2.Share.Url != info.Url || res2.Share.Version != state.Version+1 {
		t.Fatalf("refresh %v %v", res2, err)
	}
	_, res = se.data(id)
	var published, previewed publicDoc
	_ = json.Unmarshal(res.body, &published)
	_ = json.Unmarshal([]byte(fresh.ContentJson), &previewed)
	if string(res.body) != fresh.ContentJson {
		a, _ := json.Marshal(published)
		b, _ := json.Marshal(previewed)
		if !bytes.Equal(a, b) {
			t.Fatalf("published page differs from the preview:\n%s\n%s", res.body, fresh.ContentJson)
		}
	}
	if !strings.Contains(string(res.body), "新笔记") || !strings.Contains(string(res.body), "厦门市") {
		t.Fatalf("refresh did not publish the new content: %s", res.body)
	}
	if _, err = se.share.GetShare(ctx, bearer(tok, &tripmapv1.GetShareRequest{})); err != nil {
		t.Fatal(err)
	}
}

func TestShareFieldChangesHideImmediatelyAndAddOnlyWithConfirmation(t *testing.T) {
	se := newShareEnv(t)
	tok := se.register("share_fields")
	rev := se.footprint(tok)
	info := se.publish(tok, settings("link", true, true, true, false))
	id := tokenOf(info.Url)
	// The note is edited privately, then notes are hidden: hiding must not read
	// the live records, so the published city list is unchanged apart from the note.
	rev = se.mustSync2(tok, "edit", rev, upCity("350200", "厦门市", "350000", "#e2af5c"), upTrip("350200", "trip9", "2026-03-03", "不应被发布"))
	narrowed, err := se.save(tok, info.Version, settings("link", true, true, false, false), false, 0)
	if err != nil {
		t.Fatal(err)
	}
	doc, res := se.data(id)
	if len(doc.Cities) != 1 || doc.Cities[0].Trips[0].Date != "2025-05-01" || strings.Contains(string(res.body), secretNote) || strings.Contains(string(res.body), "不应被发布") || strings.Contains(string(res.body), "厦门") {
		t.Fatalf("hiding notes: %s", res.body)
	}
	if narrowed.Share.Version != info.Version+1 || narrowed.Share.PublishedRevision != info.PublishedRevision {
		t.Fatalf("narrowing must keep the published revision: %v", narrowed.Share)
	}
	// Notes cannot come back without a confirmed refresh.
	_, err = se.save(tok, narrowed.Share.Version, settings("link", true, true, true, false), false, 0)
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = se.save(tok, narrowed.Share.Version, settings("link", true, true, true, false), true, rev-1)
	wantCode(t, err, connect.CodeAborted)
	// Hiding city names removes the cities and everything attached to them.
	hidden, err := se.save(tok, narrowed.Share.Version, settings("link", false, false, false, false), false, 0)
	if err != nil {
		t.Fatal(err)
	}
	doc, res = se.data(id)
	if len(doc.Cities) != 0 || doc.Fields.Cities || strings.Contains(string(res.body), secretCity) || strings.Contains(string(res.body), "2025-05-01") {
		t.Fatalf("hiding cities: %s", res.body)
	}
	// Re-showing cities is an addition: it needs a refresh with the current revision.
	_, err = se.save(tok, hidden.Share.Version, settings("link", true, false, false, false), false, 0)
	wantCode(t, err, connect.CodeInvalidArgument)
	again, err := se.save(tok, hidden.Share.Version, settings("link", true, false, false, false), true, rev)
	if err != nil || again.Share.Url != info.Url {
		t.Fatalf("%v %v", again, err)
	}
	doc, _ = se.data(id)
	if len(doc.Cities) != 2 || doc.Cities[0].Trips[0].Date != "" || doc.Cities[0].Trips[0].Note != "" {
		t.Fatalf("doc %+v", doc)
	}
	// link -> public keeps the token, and unchanged settings change no version.
	pub, err := se.save(tok, again.Share.Version, settings("public", true, false, false, false), false, 0)
	if err != nil || pub.Share.Url != info.Url || pub.Share.Version != again.Share.Version+1 {
		t.Fatalf("%v %v", pub, err)
	}
	same, err := se.save(tok, pub.Share.Version, settings("public", true, false, false, false), false, 0)
	if err != nil || same.Share.Version != pub.Share.Version {
		t.Fatalf("an unchanged save bumped the version: %v %v", same, err)
	}
	if got := se.get("/api/share/" + id + "/data").header.Get("X-Robots-Tag"); got != "index, follow" {
		t.Fatalf("public share robots = %q", got)
	}
}

func TestSharePageOpenGraphAndPreviewImage(t *testing.T) {
	se := newShareEnv(t)
	tok := se.register("share_og")
	se.footprint(tok)
	link := se.publish(tok, settings("link", true, true, true, false))
	id := tokenOf(link.Url)
	page := se.get("/s/"+id, "User-Agent", "facebookexternalhit/1.1")
	html := string(page.body)
	if page.status != 200 || !strings.Contains(page.header.Get("Content-Type"), "text/html") {
		t.Fatalf("page %d %v", page.status, page.header)
	}
	for _, want := range []string{
		`<meta property="og:title"`, `<meta property="og:description" content="已探索 2 / 34 个省份，打卡 1 座城市，共 1 次旅行。"`,
		`<meta property="og:image" content="` + shareBase + `/s/` + id + `/og.png"`, `og:image:width" content="1200"`, `og:image:height" content="630"`,
		`<meta property="og:url" content="` + shareBase + `/s/` + id + `"`, `twitter:card" content="summary_large_image"`, `name="robots" content="noindex, nofollow"`,
		`src="/assets/index.js"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	for _, gone := range []string{"原始标题", "旧描述", "旧标题"} {
		if strings.Contains(html, gone) {
			t.Errorf("page keeps %q from the index", gone)
		}
	}
	assertNoSecrets(t, "page", page.body, se.secrets("share_og", secretCity, secretNote, "2025-05-01"))
	if page.header.Get("X-Robots-Tag") != "noindex, nofollow" || page.header.Get("Cache-Control") != "no-store" || page.header.Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(page.header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || page.header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("page headers %v", page.header)
	}
	img := se.get("/s/" + id + "/og.png")
	cfg, err := png.DecodeConfig(bytes.NewReader(img.body))
	if err != nil || cfg.Width != 1200 || cfg.Height != 630 || img.header.Get("Content-Type") != "image/png" || img.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("og.png %d %v %+v %v", img.status, img.header, cfg, err)
	}
	// The rendered preview is cached per version, but only after the lookup.
	if again := se.get("/s/" + id + "/og.png"); !bytes.Equal(again.body, img.body) {
		t.Fatal("cached preview differs")
	}
	pub, err := se.save(tok, link.Version, settings("public", true, true, true, false), false, 0)
	if err != nil {
		t.Fatal(err)
	}
	page = se.get("/s/" + id)
	if !strings.Contains(string(page.body), `name="robots" content="index, follow"`) || page.header.Get("X-Robots-Tag") != "index, follow" {
		t.Fatalf("public page is still noindex: %s", page.header)
	}
	_ = pub
	// An unavailable or failing index falls back to a minimal page that keeps the tags.
	se.indexCode.Store(http.StatusInternalServerError)
	se2 := newShareEnv(t)
	se2.indexCode.Store(http.StatusInternalServerError)
	tok2 := se2.register("share_og2")
	se2.footprint(tok2)
	id2 := tokenOf(se2.publish(tok2, settings("link", false, false, false, false)).Url)
	fallback := se2.get("/s/" + id2)
	if fallback.status != 200 || !strings.Contains(string(fallback.body), `og:image" content="`+shareBase+`/s/`+id2+`/og.png"`) || strings.Contains(string(fallback.body), "assets/index.js") {
		t.Fatalf("fallback page %d: %s", fallback.status, fallback.body)
	}
}

func TestShareIndexFetchCannotRecurseAndIsCached(t *testing.T) {
	se := newShareEnv(t)
	tok := se.register("share_loop")
	se.footprint(tok)
	id := tokenOf(se.publish(tok, settings("link", false, false, false, false)).Url)
	// A request carrying the backend's own marker is refused: a misrouted index
	// URL cannot loop back into /s/.
	missing := se.get("/s/" + strings.Repeat("a", 43))
	if loop := se.get("/s/"+id, "X-Tripmap-Index-Fetch", "1"); loop.status != 404 || !bytes.Equal(loop.body, missing.body) {
		t.Fatalf("marked request got %d", loop.status)
	}
	before := se.indexHits.Load()
	for i := 0; i < 5; i++ {
		se.get("/s/" + id)
	}
	if hits := se.indexHits.Load() - before; hits > 1 {
		t.Fatalf("index fetched %d times for 5 pages", hits)
	}
}

func TestShareRevocationClosesEveryRouteWithOneUniform404(t *testing.T) {
	se := newShareEnv(t)
	tok := se.register("share_revoke")
	se.footprint(tok)
	media := se.uploadPhoto(tok, "")
	info := se.publish(tok, settings("public", true, true, true, true, media))
	id := tokenOf(info.Url)
	doc, _ := se.data(id)
	handle := doc.Cities[0].Photos[0].ID
	routes := func(token string) map[string]string {
		return map[string]string{
			"page": "/s/" + token, "og": "/s/" + token + "/og.png", "data": "/api/share/" + token + "/data",
			"thumb": "/api/share/" + token + "/photos/" + handle, "full": "/api/share/" + token + "/photos/" + handle + "?size=full",
		}
	}
	for name, path := range routes(id) {
		if res := se.get(path); res.status != 200 {
			t.Fatalf("%s before revoke: %d", name, res.status)
		}
	}
	revoked, err := se.share.RevokeShare(context.Background(), bearer(tok, &tripmapv1.RevokeShareRequest{MutationId: "revoke-1"}))
	if err != nil || revoked.Msg.Share.Url != "" || revoked.Msg.Share.Settings.Visibility != "private" || revoked.Msg.Share.Published {
		t.Fatalf("revoke %v %v", revoked, err)
	}
	row := se.store.DB.Share.Query().OnlyX(context.Background())
	if row.Token != nil || len(row.Payload) != 0 || row.Visibility != entshare.VisibilityPrivate || !row.ShowCities {
		t.Fatalf("a private draft keeps settings only: %+v", row)
	}
	reference := se.get("/s/" + strings.Repeat("Z", 43))
	for name, path := range routes(id) {
		uniform(t, name+" after revoke", se.get(path), reference)
	}
	for name, path := range map[string]string{
		"bad format": "/s/short", "slash": "/s/", "deep": "/s/" + id + "/x/y", "wrong file": "/s/" + id + "/og.jpg", "api root": "/api/share/",
		"api unknown": "/api/share/" + id + "/other", "api no token": "/api/share/" + "/data", "photo deep": "/api/share/" + id + "/photos/a/b",
	} {
		uniform(t, name, se.get(path), reference)
	}
	// Re-enabling issues a brand-new token; the old one never works again.
	again := se.publish(tok, settings("link", true, true, true, true, media))
	if tokenOf(again.Url) == id || again.Version != 3 {
		t.Fatalf("token reused or version %d: %v", again.Version, again)
	}
	uniform(t, "old link after re-enable", se.get("/api/share/"+id+"/data"), reference)
	if _, res := se.data(tokenOf(again.Url)); res.status != 200 {
		t.Fatalf("new link %d", res.status)
	}
	// Revoking again, even with a new ID and nothing to revoke, succeeds without a version bump.
	if _, err = se.share.RevokeShare(context.Background(), bearer(tok, &tripmapv1.RevokeShareRequest{MutationId: "revoke-2"})); err != nil {
		t.Fatal(err)
	}
	last, err := se.share.RevokeShare(context.Background(), bearer(tok, &tripmapv1.RevokeShareRequest{MutationId: "revoke-3"}))
	if err != nil || last.Msg.Share.Version != again.Version+1 {
		t.Fatalf("%v %v", last, err)
	}
	if other := se.register("share_revoke_none"); other == "" {
		t.Fatal()
	} else if _, err = se.share.RevokeShare(context.Background(), bearer(other, &tripmapv1.RevokeShareRequest{MutationId: "none"})); err != nil {
		t.Fatalf("revoking without a share: %v", err)
	}
}

func TestSharePhotosAreChosenOneByOneAndStayRevocable(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	tok, other := se.register("share_photos"), se.register("share_photos_b")
	se.footprint(tok)
	se.footprint(other)
	chosen := se.uploadPhoto(tok, "trip1")
	unchosen := se.uploadPhoto(tok, "")
	foreign := se.uploadPhoto(other, "")
	// A PDF is never shareable; neither are other people's or unknown photos.
	pdf := []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF")
	res, err := se.media.PrepareUpload(ctx, bearer(tok, &tripmapv1.PrepareUploadRequest{RegionCode: "330100", Filename: "ticket.pdf", ContentType: "application/pdf", SizeBytes: int64(len(pdf)), UploadId: uuid.NewString()}))
	if err != nil {
		t.Fatal(err)
	}
	m := se.store.DB.MediaObject.GetX(ctx, res.Msg.Media.Id)
	_ = se.mem.Put(ctx, m.UploadKey, bytes.NewReader(pdf), int64(len(pdf)), "application/pdf")
	if _, err = se.media.CompleteUpload(ctx, bearer(tok, &tripmapv1.CompleteUploadRequest{Id: res.Msg.Media.Id})); err != nil {
		t.Fatal(err)
	}
	pendingPhoto, err := se.media.PrepareUpload(ctx, bearer(tok, preparePhoto(photo(t))))
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"pdf": res.Msg.Media.Id, "foreign": foreign, "unknown": uuid.NewString(), "pending": pendingPhoto.Msg.Media.Id} {
		_, err = se.share.PreviewShare(ctx, bearer(tok, &tripmapv1.PreviewShareRequest{Settings: settings("link", true, false, false, true, id)}))
		if codeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("%s photo previewed: %v", name, err)
		}
		_, err = se.save(tok, 0, settings("link", true, false, false, true, id), true, se.preview(tok, settings("link", false, false, false, false)).FootprintRevision)
		if codeOf(err) != connect.CodeAborted {
			t.Fatalf("%s photo published: %v", name, err)
		}
	}
	info := se.publish(tok, settings("link", true, true, false, true, chosen))
	id := tokenOf(info.Url)
	doc, res0 := se.data(id)
	if len(doc.Cities[0].Trips[0].Photos) != 1 || len(doc.Cities[0].Photos) != 0 {
		t.Fatalf("photo not attached to its trip: %s", res0.body)
	}
	assertNoSecrets(t, "data", res0.body, append(se.secrets("share_photos", chosen, unchosen), "photos/"+chosen))
	handle := doc.Cities[0].Trips[0].Photos[0].ID
	thumb := se.get("/api/share/" + id + "/photos/" + handle)
	cfg, err := jpegConfig(thumb.body)
	if thumb.status != 200 || thumb.header.Get("Content-Type") != "image/jpeg" || err != nil || cfg == 0 {
		t.Fatalf("thumb %d %v %v", thumb.status, thumb.header, err)
	}
	for h, want := range map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer", "Content-Security-Policy": "default-src 'none'", "X-Content-Type-Options": "nosniff"} {
		if thumb.header.Get(h) != want {
			t.Errorf("photo header %s = %q", h, thumb.header.Get(h))
		}
	}
	if strings.Contains(thumb.header.Get("Content-Disposition"), "西湖") {
		t.Fatal("file name leaked")
	}
	if full := se.get("/api/share/" + id + "/photos/" + handle + "?size=full"); full.status != 200 {
		t.Fatalf("full %d", full.status)
	}
	reference := se.get("/s/" + strings.Repeat("Z", 43))
	for name, path := range map[string]string{
		"media id as handle": "/api/share/" + id + "/photos/" + unchosen, "unchosen": "/api/share/" + id + "/photos/pzzzzzzzz",
		"bad size": "/api/share/" + id + "/photos/" + handle + "?size=orig",
	} {
		uniform(t, name, se.get(path), reference)
	}
	// Tokens of other shares never resolve this share's handles.
	otherInfo := se.publish(other, settings("link", true, false, false, false))
	uniform(t, "handle on another share", se.get("/api/share/"+tokenOf(otherInfo.Url)+"/photos/"+handle), reference)
	// Deleting the photo closes the share path at once, even with storage down.
	se.mem.fail = true
	_, err = se.media.DeleteMedia(ctx, bearer(tok, &tripmapv1.DeleteMediaRequest{Id: chosen}))
	wantCode(t, err, connect.CodeUnavailable)
	se.mem.fail = false
	uniform(t, "thumb after delete", se.get("/api/share/"+id+"/photos/"+handle), reference)
	uniform(t, "full after delete", se.get("/api/share/"+id+"/photos/"+handle+"?size=full"), reference)
	// The page itself stays; republishing with the deleted photo is refused.
	if _, res := se.data(id); res.status != 200 {
		t.Fatal("deleting a photo removed the page")
	}
	version := se.get1(tok).Version
	_, err = se.save(tok, version, settings("link", true, true, false, true, chosen), true, se.preview(tok, settings("link", false, false, false, false)).FootprintRevision)
	wantCode(t, err, connect.CodeAborted)
	// Nothing in the storage can be reached through a sibling: a photo uploaded
	// later is not published until it is chosen.
	late := se.uploadPhoto(tok, "")
	if doc, _ = se.data(id); len(doc.Cities[0].Photos) != 0 {
		t.Fatalf("a new photo appeared by itself: %+v (%s)", doc, late)
	}
	if n := se.store.DB.MediaObject.Query().Where(mediaobject.ID(late)).CountX(ctx); n != 1 {
		t.Fatal("test photo missing")
	}
}

func jpegConfig(b []byte) (int, error) {
	if len(b) < 3 || b[0] != 0xff || b[1] != 0xd8 {
		return 0, io.ErrUnexpectedEOF
	}
	return len(b), nil
}

func TestShareSaveAndRevokeAreIdempotentOnTheExactRequest(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	tok := se.register("share_idem")
	rev := se.footprint(tok)
	s := settings("link", true, true, false, false)
	p := se.preview(tok, s)
	req := func() *tripmapv1.SaveShareRequest {
		return &tripmapv1.SaveShareRequest{MutationId: "save-1", ExpectedVersion: 0, Settings: s, RefreshContent: true, PreviewRevision: p.FootprintRevision}
	}
	first, err := se.share.SaveShare(ctx, bearer(tok, req()))
	if err != nil || first.Msg.Replayed {
		t.Fatalf("first %v %v", first, err)
	}
	// The response is lost; the footprints change before the retry. The retry is
	// still the same request and is answered from its receipt, without republishing.
	se.mustSync2(tok, "meanwhile", rev, upTrip("330100", "trip5", "2026-05-05", "重试期间的新笔记"))
	retry, err := se.share.SaveShare(ctx, bearer(tok, req()))
	if err != nil || !retry.Msg.Replayed || retry.Msg.Share.Version != first.Msg.Share.Version || retry.Msg.Share.Url != first.Msg.Share.Url {
		t.Fatalf("retry %v %v", retry, err)
	}
	if strings.Contains(string(se.get("/api/share/"+tokenOf(first.Msg.Share.Url)+"/data").body), "重试期间") {
		t.Fatal("the retry published new content")
	}
	// Every field of the request is bound: reusing the ID with anything else is refused.
	for name, mutate := range map[string]func(*tripmapv1.SaveShareRequest){
		"settings":    func(r *tripmapv1.SaveShareRequest) { r.Settings = settings("link", true, false, false, false) },
		"visibility":  func(r *tripmapv1.SaveShareRequest) { r.Settings = settings("public", true, true, false, false) },
		"preview":     func(r *tripmapv1.SaveShareRequest) { r.PreviewRevision++ },
		"refresh":     func(r *tripmapv1.SaveShareRequest) { r.RefreshContent = false },
		"version":     func(r *tripmapv1.SaveShareRequest) { r.ExpectedVersion = 5 },
		"photo order": func(r *tripmapv1.SaveShareRequest) { r.Settings = settings("link", true, true, true, false) },
	} {
		r := req()
		mutate(r)
		_, err = se.share.SaveShare(ctx, bearer(tok, r))
		if name == "photo order" {
			name = "notes"
		}
		if codeOf(err) != connect.CodeAlreadyExists {
			t.Errorf("%s: reused mutation_id gave %v", name, err)
		}
	}
	// The same settings with the stale version are not mistaken for a replay: a new ID conflicts.
	stale := req()
	stale.MutationId = "save-2"
	_, err = se.share.SaveShare(ctx, bearer(tok, stale))
	wantCode(t, err, connect.CodeAborted)
	// Revoke is idempotent on its ID too, and an ID cannot be shared between kinds.
	r1, err := se.share.RevokeShare(ctx, bearer(tok, &tripmapv1.RevokeShareRequest{MutationId: "rv-1"}))
	if err != nil || r1.Msg.Replayed || r1.Msg.Share.Url != "" {
		t.Fatalf("%v %v", r1, err)
	}
	r2, err := se.share.RevokeShare(ctx, bearer(tok, &tripmapv1.RevokeShareRequest{MutationId: "rv-1"}))
	if err != nil || !r2.Msg.Replayed || r2.Msg.Share.Version != r1.Msg.Share.Version {
		t.Fatalf("%v %v", r2, err)
	}
	_, err = se.share.SaveShare(ctx, bearer(tok, &tripmapv1.SaveShareRequest{MutationId: "rv-1", Settings: s, RefreshContent: true}))
	wantCode(t, err, connect.CodeAlreadyExists)
	_, err = se.share.RevokeShare(ctx, bearer(tok, &tripmapv1.RevokeShareRequest{MutationId: "save-1"}))
	wantCode(t, err, connect.CodeAlreadyExists)
	// A replayed save after a revoke never brings the old page back.
	late, err := se.share.SaveShare(ctx, bearer(tok, req()))
	if err != nil || !late.Msg.Replayed || late.Msg.Share.Url != "" {
		t.Fatalf("replay after revoke: %v %v", late, err)
	}
	if res := se.get("/api/share/" + tokenOf(first.Msg.Share.Url) + "/data"); res.status != 404 {
		t.Fatal("a replay reopened a revoked page")
	}
}

func TestShareConcurrentSavesAndRevokeLeaveOneConsistentOutcome(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	tok := se.register("share_race")
	se.footprint(tok)
	p := se.preview(tok, settings("link", false, false, false, false))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			_, err := se.share.SaveShare(ctx, bearer(tok, &tripmapv1.SaveShareRequest{MutationId: uuid.NewString(), ExpectedVersion: 0, Settings: settings("link", false, false, false, false), RefreshContent: true, PreviewRevision: p.FootprintRevision}))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	ok, aborted := 0, 0
	for err := range results {
		switch codeOf(err) {
		case 0:
			ok++
		case connect.CodeAborted:
			aborted++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || aborted != 1 {
		t.Fatalf("two saves at version 0: %d ok, %d aborted", ok, aborted)
	}
	// Save racing revoke: whichever order wins, the state is either published at
	// the winning version or fully revoked, never a token without a payload.
	info := se.get1(tok)
	var wg2 sync.WaitGroup
	wg2.Go(func() {
		_, _ = se.share.SaveShare(ctx, bearer(tok, &tripmapv1.SaveShareRequest{MutationId: uuid.NewString(), ExpectedVersion: info.Version, Settings: settings("public", false, false, false, false)}))
	})
	wg2.Go(func() {
		_, _ = se.share.RevokeShare(ctx, bearer(tok, &tripmapv1.RevokeShareRequest{MutationId: uuid.NewString()}))
	})
	wg2.Wait()
	row := se.store.DB.Share.Query().OnlyX(ctx)
	if (row.Token == nil) != (len(row.Payload) == 0) || (row.Token == nil) != (row.Visibility == entshare.VisibilityPrivate) {
		t.Fatalf("inconsistent share row: token=%v payload=%d visibility=%s", row.Token != nil, len(row.Payload), row.Visibility)
	}
}

func TestShareAccountDeletionClosesAccessBeforeStorageCleanup(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	tok := se.register("share_delete")
	se.footprint(tok)
	media := se.uploadPhoto(tok, "")
	info := se.publish(tok, settings("public", true, true, true, true, media))
	id := tokenOf(info.Url)
	doc, _ := se.data(id)
	handle := doc.Cities[0].Photos[0].ID
	se.mem.fail = true
	_, err := se.auth.DeleteAccount(ctx, bearer(tok, &tripmapv1.DeleteAccountRequest{Confirmation: "DELETE", Password: "correct horse battery"}))
	wantCode(t, err, connect.CodeUnavailable)
	// Object cleanup failed and the account is only suspended, yet nothing is public.
	reference := se.get("/s/" + strings.Repeat("Z", 43))
	for _, path := range []string{"/s/" + id, "/s/" + id + "/og.png", "/api/share/" + id + "/data", "/api/share/" + id + "/photos/" + handle} {
		uniform(t, path, se.get(path), reference)
	}
	if n := se.store.DB.Share.Query().CountX(ctx); n != 0 {
		t.Fatalf("share row survived the suspension: %d", n)
	}
	// A pending-deletion session cannot publish again.
	_, err = se.save(tok, 0, settings("link", false, false, false, false), true, 0)
	wantCode(t, err, connect.CodeUnauthenticated)
	se.mem.fail = false
	if _, err = se.auth.DeleteAccount(ctx, bearer(tok, &tripmapv1.DeleteAccountRequest{Confirmation: "DELETE", Password: "correct horse battery"})); err != nil {
		t.Fatal(err)
	}
	uniform(t, "after deletion", se.get("/api/share/"+id+"/data"), reference)
}

func TestShareDisabledAccountIsNotReachableAndDisablingConfigClosesEverything(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	tok := se.register("share_disabled")
	se.footprint(tok)
	id := tokenOf(se.publish(tok, settings("link", false, false, false, false)).Url)
	reference := se.get("/s/" + strings.Repeat("Z", 43))
	if _, res := se.data(id); res.status != 200 {
		t.Fatal("share not reachable")
	}
	uid := se.store.DB.User.Query().Where(user.UsernameEQ("share_disabled")).OnlyX(ctx).ID
	se.store.DB.User.UpdateOneID(uid).SetDisabled(true).ExecX(ctx)
	uniform(t, "disabled account", se.get("/api/share/"+id+"/data"), reference)
	uniform(t, "disabled account og", se.get("/s/"+id+"/og.png"), reference)
	se.store.DB.User.UpdateOneID(uid).SetDisabled(false).ExecX(ctx)
	if _, res := se.data(id); res.status != 200 {
		t.Fatal("re-enabled account is not reachable")
	}
	// Turning sharing off in the configuration closes the public routes and the owner methods.
	se.cfg.Share = config.ShareConfig{}
	uniform(t, "sharing disabled", se.get("/api/share/"+id+"/data"), reference)
	uniform(t, "sharing disabled page", se.get("/s/"+id), reference)
	g, err := se.share.GetShare(ctx, bearer(tok, &tripmapv1.GetShareRequest{}))
	if err != nil || g.Msg.Enabled || g.Msg.Share.Url != "" {
		t.Fatalf("%v %v", g, err)
	}
	_, err = se.save(tok, 1, settings("link", false, false, false, false), true, 0)
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = se.share.PreviewShare(ctx, bearer(tok, &tripmapv1.PreviewShareRequest{Settings: settings("link", false, false, false, false)}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	// Revoking still works while sharing is off.
	if _, err = se.share.RevokeShare(ctx, bearer(tok, &tripmapv1.RevokeShareRequest{MutationId: "off-revoke"})); err != nil {
		t.Fatal(err)
	}
	status, err := application.NewSystemService(health.New(), func() bool { return se.cfg.Share.Enabled() }).GetStatus(ctx, &tripmapv1.GetStatusRequest{})
	if err != nil || status.ShareEnabled {
		t.Fatalf("status %v %v", status, err)
	}
}

func TestShareRateLimitsGuessingAndFailsClosedWithoutRedis(t *testing.T) {
	se := newShareEnv(t, func(c *config.AppConfig) {
		c.Share.MissRatePerMinute = 3
		c.Share.PageRatePerMinute = 4
		c.Auth.ClientIPHeader = "X-Real-IP"
	})
	tok := se.register("share_limits")
	se.footprint(tok)
	id := tokenOf(se.publish(tok, settings("link", false, false, false, false)).Url)
	guess := func(ip string) int {
		return se.get("/api/share/"+strings.Repeat("g", 43)+"/data", "X-Real-IP", ip).status
	}
	for i := 0; i < 3; i++ {
		if got := guess("203.0.113.9"); got != 404 {
			t.Fatalf("miss %d: %d", i, got)
		}
	}
	// After the third miss the address is cut off, even for a valid link; another address is not.
	if got := se.get("/api/share/"+id+"/data", "X-Real-IP", "203.0.113.9"); got.status != http.StatusTooManyRequests {
		t.Fatalf("guessing client got %d", got.status)
	}
	for i := 0; i < 4; i++ {
		if got := se.get("/api/share/"+id+"/data", "X-Real-IP", "198.51.100.7"); got.status != 200 {
			t.Fatalf("honest client %d: %d", i, got.status)
		}
	}
	if got := se.get("/api/share/"+id+"/data", "X-Real-IP", "198.51.100.7"); got.status != http.StatusTooManyRequests {
		t.Fatalf("page budget not enforced: %d", got.status)
	}
	// Redis down: public routes close with 503, they never open.
	broken := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = broken.Close() })
	se.handlers.SetResources(&app.Resources{DB: se.store.DB, Redis: broken, Blobs: se.mem, KeyPrefix: se.store.KeyPrefix,
		Health: se.resources.Health})
	for _, path := range []string{"/s/" + id, "/s/" + id + "/og.png", "/api/share/" + id + "/data", "/api/share/" + id + "/photos/pxxxxxxxx"} {
		if res := se.get(path, "X-Real-IP", "192.0.2.77"); res.status != http.StatusServiceUnavailable || bytes.Contains(res.body, []byte("og:")) {
			t.Fatalf("%s with Redis down: %d", path, res.status)
		}
	}
	se.handlers.SetResources(se.resources)
}

func TestSharePreviewAndSaveAreRateLimitedPerUser(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	tok := se.register("share_budget")
	s := &tripmapv1.PreviewShareRequest{Settings: settings("link", false, false, false, false)}
	limited := 0
	for i := 0; i < 32; i++ {
		if _, err := se.share.PreviewShare(ctx, bearer(tok, s)); codeOf(err) == connect.CodeResourceExhausted {
			limited++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if limited != 2 {
		t.Fatalf("%d previews limited, want 2 over the budget of 30", limited)
	}
	for i := 0; i < 22; i++ {
		_, err := se.share.RevokeShare(ctx, bearer(tok, &tripmapv1.RevokeShareRequest{MutationId: uuid.NewString()}))
		if i < 20 && err != nil {
			t.Fatal(err)
		}
		if i >= 20 {
			wantCode(t, err, connect.CodeResourceExhausted)
		}
	}
}

func TestSharePayloadSizeLimitAndSchemaOneAccounts(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	// A schema 1 account (legacy client) shares through its legacy city records.
	tok := se.register("share_legacy")
	if _, err := se.foot.UpsertVisit(ctx, bearer(tok, &tripmapv1.UpsertVisitRequest{Visit: &tripmapv1.Visit{Code: "330100", Name: secretCity, Province: "330000", Color: "#4b937d", Date: "2024-04-04", Note: "旧账号笔记"}})); err != nil {
		t.Fatal(err)
	}
	info := se.publish(tok, settings("link", true, true, true, false))
	doc, res := se.data(tokenOf(info.Url))
	if len(doc.Cities) != 1 || doc.Cities[0].Trips[0].Note != "旧账号笔记" || doc.Cities[0].Trips[0].Date != "2024-04-04" || doc.Stats.Trips != 1 {
		t.Fatalf("legacy account: %s", res.body)
	}
	if strings.Contains(string(res.body), "legacy-") {
		t.Fatal("legacy trip ID leaked")
	}
}

func TestSharePayloadTooLargeIsRefusedNotTruncated(t *testing.T) {
	se := newShareEnv(t)
	ctx := context.Background()
	big := se.register("share_big")
	se.mustSync2(big, "big-city", 0, upCity("330100", secretCity, "330000", "#4b937d"))
	uid := se.store.DB.User.Query().Where(user.UsernameEQ("share_big")).OnlyX(ctx).ID
	city := se.store.DB.CityVisit.Query().OnlyX(ctx)
	// Incompressible 1000-character notes, enough of them to pass the 4 MiB stored limit.
	raw := make([]byte, 750)
	var creates []*ent.TripCreate
	for i := 0; i < 7000; i++ {
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		creates = append(creates, se.store.DB.Trip.Create().SetUserID(uid).SetVisitID(city.ID).SetTripID(fmt.Sprintf("big-%d", i)).SetRegionCode("330100").
			SetNote(base64.StdEncoding.EncodeToString(raw)))
	}
	for len(creates) > 0 {
		n := min(len(creates), 500)
		se.store.DB.Trip.CreateBulk(creates[:n]...).ExecX(ctx)
		creates = creates[n:]
	}
	// Hiding the notes fits; showing them does not.
	if res := se.publish(big, settings("link", true, false, false, false)); res.Url == "" {
		t.Fatal("cities-only page refused")
	}
	_, err := se.share.PreviewShare(ctx, bearer(big, &tripmapv1.PreviewShareRequest{Settings: settings("link", true, false, true, false)}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	p := se.preview(big, settings("link", true, false, false, false))
	_, err = se.save(big, 1, settings("link", true, false, true, false), true, p.FootprintRevision)
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestSharePathsNeverReachTheApplication(t *testing.T) {
	se := newShareEnv(t)
	reference := se.get("/s/" + strings.Repeat("Z", 43))
	if reference.status != 404 {
		t.Fatalf("reference %d", reference.status)
	}
	for _, path := range []string{"/s", "/s/", "/s//", "/s/..%2f..", "/s/%2e%2e/x", "/s/" + strings.Repeat("a", 44), "/s/" + strings.Repeat("a", 43) + "/og.png/extra"} {
		res := se.get(path)
		if res.status == 200 || bytes.Contains(res.body, []byte(`id="root"`)) {
			t.Errorf("%s served the application: %d", path, res.status)
		}
		if res.status == 404 {
			uniform(t, path, res, reference)
		}
	}
	// The share routes are GET only.
	req, _ := http.NewRequest(http.MethodPost, se.server.URL+"/s/"+strings.Repeat("a", 43), strings.NewReader("{}"))
	res, err := se.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode == 200 {
		t.Fatal("POST accepted")
	}
}
