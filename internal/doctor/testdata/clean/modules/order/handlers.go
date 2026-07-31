package order

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
)

// Handlers are thin: extract the input, delegate to the service, render. No
// business rule and no repository access lives here.

func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	actor, err := m.subject(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := m.svc.List(r.Context(), actor, data.Query{
		Limit:  limit,
		Cursor: r.URL.Query().Get("cursor"),
		Sort:   r.URL.Query().Get("sort"),
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) show(w http.ResponseWriter, r *http.Request) {
	actor, err := m.subject(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
		return
	}

	found, err := m.svc.Get(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, found)
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) {
	actor, err := m.subject(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
		return
	}

	in := CreateRequest{
		Reference: r.PostFormValue("reference"),
	}

	// arandu:begin custom
	// Non-string fields are parsed here: strconv, time.Parse, and so on.
	// arandu:end custom

	created, err := m.svc.Create(r.Context(), actor, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (m *Module) destroy(w http.ResponseWriter, r *http.Request) {
	actor, err := m.subject(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
		return
	}

	if err := m.svc.Delete(r.Context(), actor, r.PathValue("id")); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail turns a domain error into a status code, in one place.
//
// Note what it does not do: it never writes the authorization error into the
// response. Why a policy said no is information about the system, and it belongs
// in the log.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	var invalid validation.Errors
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": invalid})
	case errors.Is(err, security.ErrForbidden):
		observability.Log(r.Context()).Warn("authorization denied", "error", err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case errors.Is(err, ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already exists"})
	case errors.Is(err, ErrSort):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		// Anything unrecognized is a bug, and a bug in development belongs on the
		// debug page with its queries and dumps.
		panic(err)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
