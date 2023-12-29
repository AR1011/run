package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/anthdm/ffaas/pkg/config"
	"github.com/anthdm/ffaas/pkg/storage"
	"github.com/anthdm/ffaas/pkg/types"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Server serves the public ffaas API.
type Server struct {
	router *chi.Mux
	store  storage.Store
	cache  storage.ModCacher
}

// NewServer returns a new server given a Store interface.
func NewServer(store storage.Store, cache storage.ModCacher) *Server {
	return &Server{
		store: store,
		cache: cache,
	}
}

// Listen starts listening on the given address.
func (s *Server) Listen(addr string) error {
	s.initRouter()
	return http.ListenAndServe(addr, s.router)
}

func (s *Server) initRouter() {
	s.router = chi.NewRouter()
	s.router.Get("/status", UseTimerMiddleware(handleStatus))
	s.router.Get("/application/{appID}", UseTimerMiddleware(makeAPIHandler(s.handleGetApplication)))
	s.router.Post("/application", UseTimerMiddleware(makeAPIHandler(s.handleCreateApp)))
	s.router.Post("/application/{appID}/deploy", UseTimerMiddleware(makeAPIHandler(s.handleCreateDeploy)))
	s.router.Post("/application/{appID}/rollback", UseTimerMiddleware(makeAPIHandler(s.handleCreateRollback)))
	s.router.Get("/application/{appID}/logs", UseTimerMiddleware(makeAPIHandler(s.handleGetLogs)))
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	status := map[string]string{
		"status": "ok",
	}
	json.NewEncoder(w).Encode(status)
}

// CreateAppParams holds all the necessary fields to create a new ffaas application.
type CreateAppParams struct {
	Name        string            `json:"name"`
	Environment map[string]string `json:"environment"`
}

func (p CreateAppParams) validate() error {
	if len(p.Name) < 3 || len(p.Name) > 20 {
		return fmt.Errorf("name of the application should be longer than 3 and less than 40 characters")
	}
	return nil
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) error {
	var params CreateAppParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(ErrDecodeRequestBody))
	}

	defer r.Body.Close()

	if err := params.validate(); err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(err))
	}

	app := types.NewApplication(params.Name, params.Environment)
	app.Endpoint = config.GetWasmUrl() + "/" + app.ID.String()

	if err := s.store.CreateApplication(app); err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(err))
	}

	return writeJSON(w, http.StatusOK, app)
}

// CreateDeployParams holds all the necessary fields to deploy a new function.
type CreateDeployParams struct{}

func (s *Server) handleCreateDeploy(w http.ResponseWriter, r *http.Request) error {
	appID, err := uuid.Parse(chi.URLParam(r, "appID"))
	if err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(err))
	}
	app, err := s.store.GetApplication(appID)
	if err != nil {
		return writeJSON(w, http.StatusNotFound, ErrorResponse(err))
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		return writeJSON(w, http.StatusNotFound, ErrorResponse(err))
	}
	deploy := types.NewDeploy(app, b)
	if err := s.store.CreateDeploy(deploy); err != nil {
		return writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse(err))
	}
	// Each new deploy will be the app's active deploy
	err = s.store.UpdateApplication(appID, storage.UpdateAppParams{
		ActiveDeployID: deploy.ID,
	})
	if err != nil {
		return writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse(err))
	}
	return writeJSON(w, http.StatusOK, deploy)
}

func (s *Server) handleGetApplication(w http.ResponseWriter, r *http.Request) error {
	appID, err := uuid.Parse(chi.URLParam(r, "appID"))
	if err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(err))
	}
	app, err := s.store.GetApplication(appID)
	if err != nil {
		return writeJSON(w, http.StatusNotFound, ErrorResponse(err))
	}
	return writeJSON(w, http.StatusOK, app)
}

// CreateRollbackParams holds all the necessary fields to rollback your application
// to a specific deploy id (version).
type CreateRollbackParams struct {
	DeployID uuid.UUID `json:"deploy_id"`
}

func (s *Server) handleCreateRollback(w http.ResponseWriter, r *http.Request) error {
	appID, err := uuid.Parse(chi.URLParam(r, "appID"))
	if err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(err))
	}
	app, err := s.store.GetApplication(appID)
	if err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(err))
	}

	currentDeployID := app.ActiveDeployID

	var params CreateRollbackParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(err))
	}

	deploy, err := s.store.GetDeploy(params.DeployID)
	if err != nil {
		return writeJSON(w, http.StatusNotFound, ErrorResponse(err))
	}

	updateParams := storage.UpdateAppParams{
		ActiveDeployID: deploy.ID,
	}
	if err := s.store.UpdateApplication(appID, updateParams); err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(err))
	}

	s.cache.Delete(currentDeployID)

	return writeJSON(w, http.StatusOK, map[string]any{"deploy": deploy.ID})
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) error {
	appID, err := uuid.Parse(chi.URLParam(r, "appID"))
	if err != nil {
		return writeJSON(w, http.StatusBadRequest, ErrorResponse(err))
	}

	logs, err := s.store.GetApplicationLogs(appID)
	if err != nil {
		return writeJSON(w, http.StatusNotFound, ErrorResponse(err))
	}

	return writeJSON(w, http.StatusOK, logs)

}
