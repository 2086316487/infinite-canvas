package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/basketikun/infinite-canvas/config"
	"github.com/basketikun/infinite-canvas/model"
	"github.com/basketikun/infinite-canvas/repository"
)

type ImageTaskCreateResult struct {
	ID     string                `json:"id"`
	Status model.ImageTaskStatus `json:"status"`
}

type ImageTaskModeHint string

const (
	ImageTaskModeGeneration ImageTaskModeHint = "generation"
	ImageTaskModeEdit       ImageTaskModeHint = "edit"
)

const (
	imageTaskDispatchLimit        = 8
	imageTaskDispatchInterval     = 5 * time.Second
	imageTaskLeaseDuration        = 90 * time.Second
	imageTaskLeaseRenewInterval   = 30 * time.Second
	imageTaskInitialRetryDelay    = 5 * time.Second
	imageTaskRetryDelayIncrement  = 10 * time.Second
	imageTaskMaxAttempts          = 3
	imageTaskHTTPTimeout          = 10 * time.Minute
)

var (
	imageTaskRunner        sync.Map
	imageTaskSchedulerOnce sync.Once
	imageTaskWorkerID      = newID("imgtask-worker")
	imageTaskHTTPClient    = &http.Client{Timeout: imageTaskHTTPTimeout}
)

type imageTaskExecutionError struct {
	message   string
	retryable bool
}

type imageTaskExecutionResult struct {
	task model.ImageTask
	err  error
}

func (err imageTaskExecutionError) Error() string {
	return err.message
}

func (err imageTaskExecutionError) SafeMessage() string {
	return err.message
}

func CreateImageTask(user model.AuthUser, body []byte, contentType string, mode ImageTaskModeHint) (ImageTaskCreateResult, error) {
	modelName, err := readImageTaskModel(body, contentType)
	if err != nil {
		return ImageTaskCreateResult{}, err
	}
	if strings.TrimSpace(modelName) == "" {
		return ImageTaskCreateResult{}, safeMessageError{message: "Missing model name"}
	}

	count := readImageTaskCount(body, contentType)
	credits, err := ModelCost(modelName)
	if err != nil {
		return ImageTaskCreateResult{}, err
	}
	credits *= count

	channel, err := SelectModelChannel(modelName)
	if err != nil {
		return ImageTaskCreateResult{}, err
	}

	upstreamPath := "/images/generations"
	taskMode := model.ImageTaskModeGeneration
	if mode == ImageTaskModeEdit {
		upstreamPath = "/images/edits"
		taskMode = model.ImageTaskModeEdit
	}
	upstreamPath = resolveImageTaskPath(channel.BaseURL, modelName, upstreamPath)

	if err := ConsumeUserCredits(user.ID, modelName, credits, upstreamPath); err != nil {
		return ImageTaskCreateResult{}, err
	}

	task := model.ImageTask{
		ID:          newID("imgtask"),
		UserID:      user.ID,
		Mode:        taskMode,
		Model:       modelName,
		Count:       count,
		Status:      model.ImageTaskStatusPending,
		Credits:     credits,
		ContentType: contentType,
		RequestBody: append([]byte(nil), body...),
		UpstreamPath: upstreamPath,
		CreatedAt:   now(),
		UpdatedAt:   now(),
	}
	if _, err := repository.SaveImageTask(task); err != nil {
		_ = RefundUserCredits(user.ID, modelName, credits, upstreamPath)
		return ImageTaskCreateResult{}, err
	}

	if config.ImageTaskWorkerEnabled() {
		go RunImageTask(task.ID)
	}
	return ImageTaskCreateResult{ID: task.ID, Status: task.Status}, nil
}

func GetImageTaskForUser(userID string, id string) (model.ImageTask, error) {
	task, ok, err := repository.GetImageTaskByID(id)
	if err != nil {
		return model.ImageTask{}, err
	}
	if !ok || task.UserID != userID {
		return model.ImageTask{}, safeMessageError{message: "Task not found"}
	}
	return sanitizeImageTask(task), nil
}

func ListImageTasksForUser(userID string, limit int) ([]model.ImageTask, error) {
	items, err := repository.ListImageTasksByUser(userID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]model.ImageTask, 0, len(items))
	for _, item := range items {
		result = append(result, sanitizeImageTask(item))
	}
	return result, nil
}

func StartImageTaskScheduler() error {
	if err := ResumePendingImageTasks(); err != nil {
		return err
	}
	imageTaskSchedulerOnce.Do(func() {
		go runImageTaskSchedulerLoop()
	})
	return nil
}

func RunImageTaskScheduler() error {
	if err := ResumePendingImageTasks(); err != nil {
		return err
	}
	runImageTaskSchedulerLoop()
	return nil
}

func ResumePendingImageTasks() error {
	ids, err := repository.ListDueImageTaskIDs(imageTaskDispatchLimit, now())
	if err != nil {
		return err
	}
	for _, id := range ids {
		go RunImageTask(id)
	}
	return nil
}

func runImageTaskSchedulerLoop() {
	ticker := time.NewTicker(imageTaskDispatchInterval)
	defer ticker.Stop()
	for range ticker.C {
		if err := ResumePendingImageTasks(); err != nil {
			log.Printf("image task dispatch failed: err=%v", err)
		}
	}
}

func RunImageTask(id string) {
	if _, loaded := imageTaskRunner.LoadOrStore(id, struct{}{}); loaded {
		return
	}
	defer imageTaskRunner.Delete(id)

	task, claimed, err := claimImageTask(id)
	if err != nil {
		log.Printf("image task claim failed: id=%s err=%v", id, err)
		return
	}
	if !claimed {
		return
	}

	stopLease := startImageTaskLeaseRenewer(task.ID)
	defer stopLease()

	if len(task.Outputs) == 0 {
		body, ok, err := loadImageTaskRecovery(task.ID)
		if err != nil {
			log.Printf("image task recovery load failed: id=%s err=%v", id, err)
			handleImageTaskError(task, newImageTaskExecutionError("Load generated image recovery failed", true))
			return
		}
		if ok {
			outputs, parseErr := parseImageTaskOutputs(body)
			if parseErr != nil {
				log.Printf("image task recovery parse failed: id=%s err=%v", id, parseErr)
				handleImageTaskError(task, parseErr)
				return
			}
			outputs, persistErr := persistImageTaskOutputs(task.ID, outputs)
			if persistErr != nil {
				log.Printf("image task recovery persist failed: id=%s err=%v", id, persistErr)
				handleImageTaskError(task, persistErr)
				return
			}
			finishedAt := now()
			ok, saveErr := repository.UpdateImageTaskByOwner(task.ID, imageTaskWorkerID, map[string]any{
				"status":        model.ImageTaskStatusSucceeded,
				"error_message": "",
				"outputs":       outputs,
				"finished_at":   finishedAt,
				"locked_by":     "",
				"locked_until":  "",
				"next_run_at":   "",
				"updated_at":    finishedAt,
			})
			if saveErr != nil {
				log.Printf("image task recovery save failed: id=%s err=%v", id, saveErr)
				return
			}
			if !ok {
				log.Printf("image task recovery lease lost: id=%s", id)
				return
			}
			_ = removeImageTaskRecovery(task.ID)
			return
		}
	}

	if task.Attempts > imageTaskMaxAttempts {
		handleImageTaskError(task, newImageTaskExecutionError("AI request timed out", false))
		return
	}

	var execErr error
	task, execErr = executeImageTaskWithTimeout(task)
	if execErr != nil {
		handleImageTaskError(task, execErr)
		return
	}

	finishedAt := now()
	ok, err := repository.UpdateImageTaskByOwner(task.ID, imageTaskWorkerID, map[string]any{
		"status":        model.ImageTaskStatusSucceeded,
		"error_message": "",
		"outputs":       task.Outputs,
		"finished_at":   finishedAt,
		"locked_by":     "",
		"locked_until":  "",
		"next_run_at":   "",
		"updated_at":    finishedAt,
	})
	if err != nil {
		log.Printf("image task success state save failed: id=%s err=%v", id, err)
		return
	}
	if !ok {
		log.Printf("image task success state lease lost: id=%s", id)
		return
	}
	if err := removeImageTaskRecovery(task.ID); err != nil {
		log.Printf("image task recovery cleanup failed: id=%s err=%v", id, err)
	}
}

func executeImageTaskWithTimeout(task model.ImageTask) (model.ImageTask, error) {
	ctx, cancel := context.WithTimeout(context.Background(), imageTaskHTTPTimeout)
	defer cancel()
	resultCh := make(chan imageTaskExecutionResult, 1)
	go func() {
		nextTask := task
		resultCh <- imageTaskExecutionResult{task: nextTask, err: executeImageTask(ctx, &nextTask)}
	}()
	select {
	case result := <-resultCh:
		return result.task, result.err
	case <-ctx.Done():
		return task, newImageTaskExecutionError("AI request timed out", true)
	}
}

func executeImageTask(ctx context.Context, task *model.ImageTask) error {
	started := time.Now()
	channel, err := SelectModelChannel(task.Model)
	if err != nil {
		return err
	}
	upstreamPath := resolveImageTaskPath(channel.BaseURL, task.Model, task.UpstreamPath)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, BuildModelChannelURL(channel, upstreamPath), bytes.NewReader(task.RequestBody))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+channel.APIKey)
	if task.ContentType != "" {
		request.Header.Set("Content-Type", task.ContentType)
	}

	log.Printf("image task start: id=%s model=%s upstream_host=%s upstream_path=%s", task.ID, task.Model, request.URL.Host, request.URL.Path)
	response, err := imageTaskHTTPClient.Do(request)
	if err != nil {
		log.Printf("image task request failed: id=%s duration=%s err=%v", task.ID, time.Since(started).Round(time.Millisecond), err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return newImageTaskExecutionError("AI request timed out", true)
		}
		return newImageTaskExecutionError("AI request failed", true)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return newImageTaskExecutionError("AI response read failed", true)
	}
	if response.StatusCode >= http.StatusBadRequest {
		log.Printf("image task upstream error: id=%s status=%d duration=%s", task.ID, response.StatusCode, time.Since(started).Round(time.Millisecond))
		return newImageTaskExecutionError(imageTaskUpstreamStatusMessage(response.StatusCode, body), imageTaskStatusRetryable(response.StatusCode))
	}

	outputs, err := parseImageTaskOutputs(body)
	if err != nil {
		log.Printf("image task parse failed: id=%s duration=%s err=%v", task.ID, time.Since(started).Round(time.Millisecond), err)
		return err
	}
	if err := persistImageTaskRecovery(task.ID, body); err != nil {
		log.Printf("image task recovery persist failed: id=%s duration=%s err=%v", task.ID, time.Since(started).Round(time.Millisecond), err)
		return safeMessageError{message: "Save generated image failed"}
	}
	outputs, err = persistImageTaskOutputs(task.ID, outputs)
	if err != nil {
		log.Printf("image task output persist failed: id=%s duration=%s err=%v", task.ID, time.Since(started).Round(time.Millisecond), err)
		return err
	}
	task.Outputs = outputs
	log.Printf("image task succeeded: id=%s status=%d outputs=%d bytes=%d duration=%s", task.ID, response.StatusCode, len(outputs), len(body), time.Since(started).Round(time.Millisecond))
	return nil
}

func resolveImageTaskPath(baseURL string, modelName string, path string) string {
	return path
}

func claimImageTask(id string) (model.ImageTask, bool, error) {
	lockUntil := time.Now().Add(imageTaskLeaseDuration).Format(time.RFC3339)
	claimed, err := repository.ClaimImageTask(id, imageTaskWorkerID, lockUntil, now())
	if err != nil || !claimed {
		return model.ImageTask{}, claimed, err
	}
	task, ok, err := repository.GetImageTaskByID(id)
	if err != nil {
		return model.ImageTask{}, false, err
	}
	if !ok {
		return model.ImageTask{}, false, nil
	}
	return task, true, nil
}

func startImageTaskLeaseRenewer(id string) func() {
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(imageTaskLeaseRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				lockUntil := time.Now().Add(imageTaskLeaseDuration).Format(time.RFC3339)
				ok, err := repository.RenewImageTaskLease(id, imageTaskWorkerID, lockUntil, now())
				if err != nil {
					log.Printf("image task lease renew failed: id=%s err=%v", id, err)
					continue
				}
				if !ok {
					log.Printf("image task lease renew lost: id=%s", id)
					return
				}
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(stop)
		})
	}
}

func handleImageTaskError(task model.ImageTask, err error) {
	if shouldRetryImageTask(task, err) {
		nextRunAt := time.Now().Add(imageTaskRetryDelay(task.Attempts)).Format(time.RFC3339)
		ok, saveErr := repository.UpdateImageTaskByOwner(task.ID, imageTaskWorkerID, map[string]any{
			"status":        model.ImageTaskStatusPending,
			"error_message": "",
			"locked_by":     "",
			"locked_until":  "",
			"next_run_at":   nextRunAt,
			"updated_at":    now(),
		})
		if saveErr != nil {
			log.Printf("image task retry schedule failed: id=%s err=%v", task.ID, saveErr)
		} else if !ok {
			log.Printf("image task retry lease lost: id=%s", task.ID)
		} else {
			log.Printf("image task retry scheduled: id=%s attempt=%d next_run_at=%s err=%v", task.ID, task.Attempts, nextRunAt, err)
		}
		return
	}

	finishedAt := now()
	ok, saveErr := repository.UpdateImageTaskByOwner(task.ID, imageTaskWorkerID, map[string]any{
		"status":        model.ImageTaskStatusFailed,
		"error_message": err.Error(),
		"finished_at":   finishedAt,
		"locked_by":     "",
		"locked_until":  "",
		"next_run_at":   "",
		"updated_at":    finishedAt,
	})
	if saveErr != nil {
		log.Printf("image task failed state save failed: id=%s err=%v", task.ID, saveErr)
		return
	}
	if !ok {
		log.Printf("image task failed state lease lost: id=%s", task.ID)
		return
	}
	if !task.Refunded {
		if refundErr := RefundUserCredits(task.UserID, task.Model, task.Credits, task.UpstreamPath); refundErr != nil {
			log.Printf("image task refund failed: id=%s err=%v", task.ID, refundErr)
		} else if markErr := repository.MarkImageTaskRefunded(task.ID); markErr != nil {
			log.Printf("image task refunded mark failed: id=%s err=%v", task.ID, markErr)
		}
	}
}

func shouldRetryImageTask(task model.ImageTask, err error) bool {
	return task.Attempts < imageTaskMaxAttempts && isRetryableImageTaskError(err)
}

func imageTaskRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return imageTaskInitialRetryDelay + time.Duration(attempt-1)*imageTaskRetryDelayIncrement
}

func newImageTaskExecutionError(message string, retryable bool) error {
	return imageTaskExecutionError{message: message, retryable: retryable}
}

func isRetryableImageTaskError(err error) bool {
	var target imageTaskExecutionError
	return errors.As(err, &target) && target.retryable
}

func parseImageTaskOutputs(body []byte) ([]model.ImageTaskOutput, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	urls := resolveImageDataURLsForTask(payload, 0)
	if len(urls) == 0 {
		if message := imageTaskUpstreamErrorDetail(body); message != "" {
			return nil, safeMessageError{message: message}
		}
		return nil, safeMessageError{message: "No image returned from upstream"}
	}

	revisedPrompts := extractImageTaskRevisedPrompts(payload)
	outputs := make([]model.ImageTaskOutput, 0, len(urls))
	for index, item := range urls {
		output := model.ImageTaskOutput{MimeType: "image/png"}
		if strings.HasPrefix(item, "data:image/") {
			output.DataURL = item
			output.URL = item
			if mimeType := readDataURLMimeType(item); mimeType != "" {
				output.MimeType = mimeType
			}
		} else {
			output.URL = item
		}
		if index < len(revisedPrompts) {
			output.RevisedPrompt = revisedPrompts[index]
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func resolveImageDataURLsForTask(value any, depth int) []string {
	if depth > 5 || value == nil {
		return nil
	}
	if direct := normalizeImageTaskValue(value); direct != "" {
		return []string{direct}
	}

	switch typed := value.(type) {
	case []any:
		result := []string{}
		for _, item := range typed {
			result = append(result, resolveImageDataURLsForTask(item, depth+1)...)
		}
		return result
	case map[string]any:
		directFields := []string{
			readImageTaskField(typed["b64_json"]),
			readImageTaskField(typed["url"]),
			readImageTaskField(typed["image_url"]),
			readImageTaskField(typed["imageUrl"]),
			readImageTaskField(typed["image"]),
			readImageTaskField(typed["base64"]),
			readImageTaskField(typed["b64"]),
		}
		result := []string{}
		for _, item := range directFields {
			if item != "" {
				result = append(result, item)
			}
		}
		if len(result) > 0 {
			return result
		}
		for _, key := range []string{"data", "images", "image_urls", "imageUrls", "output", "outputs", "result", "results", "artifacts", "items", "content"} {
			result = append(result, resolveImageDataURLsForTask(typed[key], depth+1)...)
		}
		return result
	default:
		return nil
	}
}

func normalizeImageTaskValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "data:image/") {
		return text
	}
	if isLikelyBase64(text) {
		return "data:image/png;base64," + compactBase64(text)
	}
	return ""
}

func readImageTaskField(value any) string {
	if direct := normalizeImageTaskValue(value); direct != "" {
		return direct
	}
	item, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return normalizeImageTaskValue(item["url"])
}

func isLikelyBase64(text string) bool {
	compacted := compactBase64(text)
	if len(compacted) <= 100 {
		return false
	}
	for _, r := range text {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '=' || r == '\r' || r == '\n' || r == '\t' || r == ' ':
		default:
			return false
		}
	}
	return true
}

func compactBase64(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t', ' ':
			return -1
		default:
			return r
		}
	}, text)
}

func extractImageTaskRevisedPrompts(payload map[string]any) []string {
	items, ok := payload["data"].([]any)
	if !ok {
		return nil
	}
	result := []string{}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, _ := object["revised_prompt"].(string)
		result = append(result, strings.TrimSpace(text))
	}
	return result
}

func readDataURLMimeType(value string) string {
	if !strings.HasPrefix(value, "data:") {
		return ""
	}
	prefix := value
	if index := strings.Index(prefix, ","); index >= 0 {
		prefix = prefix[:index]
	}
	prefix = strings.TrimPrefix(prefix, "data:")
	if index := strings.Index(prefix, ";"); index >= 0 {
		prefix = prefix[:index]
	}
	return prefix
}

func readImageTaskModel(body []byte, contentType string) (string, error) {
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return readMultipartImageTaskModel(body, contentType), nil
	}
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Model) == "" {
		return "", safeMessageError{message: "Missing model name"}
	}
	return payload.Model, nil
}

func readMultipartImageTaskModel(body []byte, contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	form, err := reader.ReadForm(32 << 20)
	if err != nil {
		return ""
	}
	defer form.RemoveAll()
	if values := form.Value["model"]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func readImageTaskCount(body []byte, contentType string) int {
	count := 1
	if strings.HasPrefix(contentType, "multipart/form-data") {
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			return count
		}
		form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(32 << 20)
		if err != nil {
			return count
		}
		defer form.RemoveAll()
		if values := form.Value["n"]; len(values) > 0 {
			_, _ = fmt.Sscan(values[0], &count)
		}
	} else {
		var payload struct {
			N int `json:"n"`
		}
		_ = json.Unmarshal(body, &payload)
		count = payload.N
	}
	if count < 1 {
		return 1
	}
	return count
}

func sanitizeImageTask(task model.ImageTask) model.ImageTask {
	task.RequestBody = nil
	task.ContentType = ""
	task.UpstreamPath = ""
	task.LockedBy = ""
	task.LockedUntil = ""
	task.NextRunAt = ""
	task.MaxAttempts = imageTaskMaxAttempts
	return task
}

func imageTaskUpstreamStatusMessage(statusCode int, body []byte) string {
	base := imageTaskStatusMessage(statusCode)
	detail := imageTaskUpstreamErrorDetail(body)
	if detail == "" {
		return base
	}
	return base + ": " + detail
}

func imageTaskStatusMessage(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "AI auth failed, please check API key or model access"
	case http.StatusTooManyRequests:
		return "AI rate limited or quota exhausted"
	default:
		return "AI request failed"
	}
}

func imageTaskStatusRetryable(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func imageTaskUpstreamErrorDetail(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	var payload struct {
		Msg     string `json:"msg"`
		Message string `json:"message"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if payload.Error.Message != "" {
			if payload.Error.Code != "" {
				return imageTaskSafeText(payload.Error.Code + " " + payload.Error.Message)
			}
			return imageTaskSafeText(payload.Error.Message)
		}
		if payload.Msg != "" {
			return imageTaskSafeText(payload.Msg)
		}
		if payload.Message != "" {
			return imageTaskSafeText(payload.Message)
		}
	}
	return imageTaskSafeText(text)
}

func imageTaskSafeText(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(text)
	if len(runes) > 300 {
		return string(runes[:300]) + "..."
	}
	return text
}
