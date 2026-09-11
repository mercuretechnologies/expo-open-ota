package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"xprem/config"
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
		RenderError(w, http.StatusNotFound, "Build or share not found.")
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

func (h *BuildRegistryHandler) Begin(w http.ResponseWriter, r *http.Request) {
	var input services.RegisterBuildInput
	if !decodeBuildBody(w, r, &input) {
		return
	}
	result, err := h.service.Begin(r.Context(), mux.Vars(r)["APP_ID"], services.BuildIdentifierFromContext(r.Context()), mux.Vars(r)["BUILD_ID"], input)
	if err != nil {
		renderBuildRegistryError(w, err)
		return
	}
	if result.LocalToken != "" {
		result.Upload.URL, err = publicBuildURL("/build-uploads/" + result.LocalToken)
		if err != nil {
			renderBuildRegistryError(w, err)
			return
		}
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
	if err := h.service.UploadLocal(r.Context(), mux.Vars(r)["TOKEN"], r.Body); err != nil {
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
