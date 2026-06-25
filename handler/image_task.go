package handler

import (
	"io"
	"net/http"
	"strconv"

	"github.com/basketikun/infinite-canvas/service"
)

func CreateImageGenerationTask(w http.ResponseWriter, r *http.Request) {
	createImageTask(w, r, service.ImageTaskModeGeneration)
}

func CreateImageEditTask(w http.ResponseWriter, r *http.Request) {
	createImageTask(w, r, service.ImageTaskModeEdit)
}

func createImageTask(w http.ResponseWriter, r *http.Request, mode service.ImageTaskModeHint) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "Unauthorized")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		Fail(w, "Failed to read AI request")
		return
	}
	result, err := service.CreateImageTask(user, body, r.Header.Get("Content-Type"), mode)
	if err != nil {
		FailError(w, err)
		return
	}
	OK(w, result)
}

func ImageTask(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "Unauthorized")
		return
	}
	task, err := service.GetImageTaskForUser(user.ID, id)
	if err != nil {
		FailError(w, err)
		return
	}
	OK(w, task)
}

func ImageTasks(w http.ResponseWriter, r *http.Request) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "Unauthorized")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := service.ListImageTasksForUser(user.ID, limit)
	if err != nil {
		FailError(w, err)
		return
	}
	OK(w, items)
}
