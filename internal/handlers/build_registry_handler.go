package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"xprem/config"
	"xprem/internal/bucket"
	"xprem/internal/services"
	"xprem/internal/store"
	"xprem/internal/types"
	"xprem/internal/validation"

	"github.com/gorilla/mux"
)

const buildRequestBodyLimit = 16 << 10

type BuildRegistryHandler struct{ service *services.BuildService }

func NewBuildRegistryHandler(service *services.BuildService) *BuildRegistryHandler {
	return &BuildRegistryHandler{service: service}
}

func renderBuildRegistryError(w http.ResponseWriter, err error) {
	var missing *store.ErrResourceNotFound
	switch {
	case errors.Is(err, services.ErrUnauthorized):
		RenderError(w, http.StatusUnauthorized, "Invalid or expired upload authorization.")
	case errors.Is(err, services.ErrBuildConflict), errors.Is(err, services.ErrBuildNotReady), errors.Is(err, services.ErrBuildState):
		RenderError(w, http.StatusConflict, err.Error())
	case errors.Is(err, services.ErrBuildIntegrity):
		RenderError(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &missing):
		RenderError(w, http.StatusNotFound, "Build not found.")
	case validation.IsValidationError(err), errors.Is(err, store.ErrNotSupportedInStatelessMode):
		RenderError(w, http.StatusBadRequest, err.Error())
	default:
		RenderError(w, http.StatusInternalServerError, "Could not process the build request.")
	}
}

// publicBuildURL appends route to BASE_URL, keeping any sub-path it is served from.
func publicBuildURL(route string) (string, error) {
	base, err := url.Parse(strings.TrimRight(config.GetEnv("BASE_URL"), "/"))
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil {
		return "", fmt.Errorf("invalid BASE_URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + route
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	return base.String(), nil
}

func decodeBuildBody(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, buildRequestBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.More() {
		RenderError(w, http.StatusBadRequest, "Invalid build request body.")
		return false
	}
	return true
}

func (h *BuildRegistryHandler) Start(w http.ResponseWriter, r *http.Request) {
	var input services.BuildStartInput
	if !decodeBuildBody(w, r, &input) {
		return
	}
	build, err := h.service.Start(r.Context(), mux.Vars(r)["APP_ID"], services.BuildIdentifierFromContext(r.Context()), mux.Vars(r)["BUILD_ID"], input)
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	RenderJSON(w, http.StatusOK, build)
}

func (h *BuildRegistryHandler) RegisterArtifact(w http.ResponseWriter, r *http.Request) {
	var input services.RegisterBuildInput
	if !decodeBuildBody(w, r, &input) {
		return
	}
	result, err := h.service.RegisterArtifact(r.Context(), mux.Vars(r)["APP_ID"], services.BuildIdentifierFromContext(r.Context()), mux.Vars(r)["BUILD_ID"], input)
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	RenderJSON(w, http.StatusOK, result)
}

func (h *BuildRegistryHandler) Fail(w http.ResponseWriter, r *http.Request) {
	var input services.FailBuildInput
	if !decodeBuildBody(w, r, &input) {
		return
	}
	build, err := h.service.Fail(r.Context(), mux.Vars(r)["APP_ID"], services.BuildIdentifierFromContext(r.Context()), mux.Vars(r)["BUILD_ID"], input)
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	RenderJSON(w, http.StatusOK, build)
}

func (h *BuildRegistryHandler) Complete(w http.ResponseWriter, r *http.Request) {
	build, err := h.service.Complete(r.Context(), mux.Vars(r)["APP_ID"], services.BuildIdentifierFromContext(r.Context()), mux.Vars(r)["BUILD_ID"])
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	RenderJSON(w, http.StatusOK, build)
}

func (h *BuildRegistryHandler) UploadLocal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, services.MaxBuildSize+1)
	if err := h.service.UploadLocal(r.Context(), mux.Vars(r)["APP_ID"], services.BuildIdentifierFromContext(r.Context()), mux.Vars(r)["BUILD_ID"], r.Header.Get(bucket.LocalUploadTokenHeader), r.Body); err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *BuildRegistryHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, offset := 20, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
	}
	if err == nil {
		if value := r.URL.Query().Get("offset"); value != "" {
			offset, err = strconv.Atoi(value)
		}
	}
	if err != nil || limit < 1 || limit > 100 || offset < 0 || offset > 100000 {
		RenderError(w, http.StatusBadRequest, "Invalid pagination.")
		return
	}
	builds, count, err := h.service.List(r.Context(), mux.Vars(r)["APP_ID"], int32(limit), int32(offset))
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	RenderJSON(w, http.StatusOK, map[string]any{"builds": builds, "count": count})
}

func (h *BuildRegistryHandler) Get(w http.ResponseWriter, r *http.Request) {
	b, err := h.service.Get(r.Context(), mux.Vars(r)["APP_ID"], mux.Vars(r)["BUILD_ID"])
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	RenderJSON(w, http.StatusOK, b)
}

func (h *BuildRegistryHandler) Download(w http.ResponseWriter, r *http.Request) {
	b, err := h.service.Get(r.Context(), mux.Vars(r)["APP_ID"], mux.Vars(r)["BUILD_ID"])
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	h.download(w, r, *b)
}

func (h *BuildRegistryHandler) download(w http.ResponseWriter, r *http.Request, b types.BuildRecord) {
	file, err := h.service.Download(r.Context(), b)
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	if file == nil {
		RenderError(w, http.StatusNotFound, "Artifact not found.")
		return
	}
	defer file.Reader.Close()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/octet-stream")
	if b.ArtifactType == "apk" {
		w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, b.ID, b.ArtifactType))
	w.Header().Set("Content-Length", strconv.FormatInt(b.Size, 10))
	_, _ = io.Copy(w, file.Reader)
}

func (h *BuildRegistryHandler) CreateShare(w http.ResponseWriter, r *http.Request) {
	input := struct {
		ExpiresInHours int `json:"expiresInHours"`
	}{24}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		RenderError(w, http.StatusBadRequest, "Invalid share expiration.")
		return
	}
	if _, err := publicBuildURL("/build-shares/"); err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	share, token, err := h.service.CreateShare(r.Context(), mux.Vars(r)["APP_ID"], mux.Vars(r)["BUILD_ID"], input.ExpiresInHours)
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	link, err := publicBuildURL("/build-shares/" + token)
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	RenderJSON(w, http.StatusCreated, map[string]any{"share": share, "url": link})
}

func (h *BuildRegistryHandler) ListShares(w http.ResponseWriter, r *http.Request) {
	shares, err := h.service.ListShares(r.Context(), mux.Vars(r)["APP_ID"], mux.Vars(r)["BUILD_ID"])
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	RenderJSON(w, http.StatusOK, map[string]any{"shares": shares})
}

func (h *BuildRegistryHandler) RevokeShare(w http.ResponseWriter, r *http.Request) {
	if err := h.service.RevokeShare(r.Context(), mux.Vars(r)["APP_ID"], mux.Vars(r)["BUILD_ID"], mux.Vars(r)["SHARE_ID"]); err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var buildInstallPage = template.Must(template.New("install").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow"><title>Install Android app</title><style>body{font:16px system-ui;background:#f6f7f9;color:#17212b;max-width:480px;margin:12vh auto;padding:24px}main{background:white;border:1px solid #ddd;border-radius:16px;padding:32px}a{display:block;text-align:center;background:#17212b;color:white;padding:14px;border-radius:8px;text-decoration:none}small{color:#536171}</style><main><h1>Install Android app</h1><p>{{.ApplicationID}}</p><p>Version {{.Version}} ({{.BuildNumber}}) · {{.Size}} MB</p><p><small>Link expires {{.ExpiresAt}}</small></p><a href="{{.Download}}">Download APK</a><p><small>Open the downloaded APK on Android. Your device may ask you to allow installation from this source.</small></p></main></html>`))

func (h *BuildRegistryHandler) PublicShare(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	token := mux.Vars(r)["TOKEN"]
	b, expiry, err := h.service.ResolveShare(r.Context(), token)
	var missing *store.ErrResourceNotFound
	switch {
	case errors.As(err, &missing), errors.Is(err, store.ErrNotSupportedInStatelessMode):
		http.Error(w, "This sharing link has expired, was revoked, or does not exist.", http.StatusGone)
		return
	case err != nil:
		http.Error(w, "Could not resolve this sharing link.", http.StatusInternalServerError)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/download") {
		h.download(w, r, *b)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	_ = buildInstallPage.Execute(w, map[string]string{"ApplicationID": b.ApplicationID, "Version": b.Metadata.Version, "BuildNumber": b.Metadata.BuildNumber, "Size": fmt.Sprintf("%.1f", float64(b.Size)/(1<<20)), "ExpiresAt": expiry.UTC().Format(time.RFC1123), "Download": token + "/download"})
}
