package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderJSONMarshalFailureReturnsProblem(t *testing.T) {
	recorder := httptest.NewRecorder()

	RenderJSON(recorder, http.StatusCreated, make(chan int))

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Equal(t, "application/problem+json; charset=utf-8", recorder.Header().Get("Content-Type"))
	var problem APIError
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &problem))
	require.Equal(t, http.StatusInternalServerError, problem.Status)
}
