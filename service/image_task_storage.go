package service

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"os"
	"path/filepath"
	"strings"

	"github.com/basketikun/infinite-canvas/config"
	"github.com/basketikun/infinite-canvas/model"
)

const generatedImageTaskURLPrefix = "/api/media/generated-images/"
const generatedImageTaskMaxBytes = 80 << 20

func GeneratedImageTaskMediaPath(id string) string {
	return filepath.Join(generatedImageTaskDir(), filepath.Base(id))
}

func persistImageTaskRecovery(taskID string, body []byte) error {
	return writeGeneratedImageTaskFile(generatedImageTaskRecoveryPath(taskID), body, 0644)
}

func loadImageTaskRecovery(taskID string) ([]byte, bool, error) {
	body, err := os.ReadFile(generatedImageTaskRecoveryPath(taskID))
	if err == nil {
		return body, true, nil
	}
	if errorsIsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func removeImageTaskRecovery(taskID string) error {
	if err := os.Remove(generatedImageTaskRecoveryPath(taskID)); err != nil && !errorsIsNotExist(err) {
		return err
	}
	return nil
}

func persistImageTaskOutputs(taskID string, outputs []model.ImageTaskOutput) ([]model.ImageTaskOutput, error) {
	if len(outputs) == 0 {
		return outputs, nil
	}
	if err := os.MkdirAll(generatedImageTaskDir(), 0755); err != nil {
		return nil, safeMessageError{message: "Save generated image failed"}
	}
	items := make([]model.ImageTaskOutput, 0, len(outputs))
	for index, output := range outputs {
		item, err := persistImageTaskOutput(taskID, index, output)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func persistImageTaskOutput(taskID string, index int, output model.ImageTaskOutput) (model.ImageTaskOutput, error) {
	source := strings.TrimSpace(output.DataURL)
	if source == "" {
		source = strings.TrimSpace(output.URL)
	}
	if source == "" {
		return output, safeMessageError{message: "No image returned from upstream"}
	}
	if isGeneratedImageTaskURL(source) {
		output.DataURL = ""
		return output, nil
	}

	data, mimeType, err := readImageTaskOutputBytes(source, output.MimeType)
	if err != nil {
		return output, err
	}
	fileName := fmt.Sprintf("%s-%03d%s", filepath.Base(taskID), index+1, generatedImageTaskExt(mimeType, source))
	if err := writeGeneratedImageTaskFile(filepath.Join(generatedImageTaskDir(), fileName), data, 0644); err != nil {
		return output, safeMessageError{message: "Save generated image failed"}
	}

	output.URL = generatedImageTaskURLPrefix + fileName
	output.DataURL = ""
	output.MimeType = mimeType
	return output, nil
}

func readImageTaskOutputBytes(source string, fallbackMimeType string) ([]byte, string, error) {
	if strings.HasPrefix(source, "data:") {
		body, mimeType, err := decodeImageTaskDataURL(source)
		if err != nil {
			return nil, "", safeMessageError{message: "Generated image data is invalid"}
		}
		if err := validateGeneratedImageTaskBytes(body); err != nil {
			return nil, "", err
		}
		mimeType = normalizeGeneratedImageTaskMimeType(mimeType, source, body, fallbackMimeType)
		if mimeType == "" {
			return nil, "", safeMessageError{message: "Generated image format is not supported"}
		}
		return body, mimeType, nil
	}
	if !strings.HasPrefix(strings.ToLower(source), "http://") && !strings.HasPrefix(strings.ToLower(source), "https://") {
		return nil, "", safeMessageError{message: "AI returned an unsupported image URL"}
	}

	request, err := http.NewRequest(http.MethodGet, source, nil)
	if err != nil {
		return nil, "", safeMessageError{message: "Generated image URL is invalid"}
	}
	response, err := imageTaskHTTPClient.Do(request)
	if err != nil {
		return nil, "", newImageTaskExecutionError("Generated image download failed", true)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return nil, "", newImageTaskExecutionError("Generated image download failed", response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, generatedImageTaskMaxBytes+1))
	if err != nil {
		return nil, "", newImageTaskExecutionError("Generated image read failed", true)
	}
	if err := validateGeneratedImageTaskBytes(body); err != nil {
		return nil, "", err
	}
	mimeType := normalizeGeneratedImageTaskMimeType(response.Header.Get("Content-Type"), source, body, fallbackMimeType)
	if mimeType == "" {
		return nil, "", safeMessageError{message: "Generated image format is not supported"}
	}
	return body, mimeType, nil
}

func decodeImageTaskDataURL(value string) ([]byte, string, error) {
	index := strings.Index(value, ",")
	if index < 0 {
		return nil, "", base64.CorruptInputError(0)
	}
	header := value[:index]
	mimeType := readDataURLMimeType(value)
	if !strings.Contains(strings.ToLower(header), ";base64") {
		return nil, mimeType, base64.CorruptInputError(0)
	}
	data, err := base64.StdEncoding.DecodeString(compactBase64(value[index+1:]))
	if err != nil {
		return nil, mimeType, err
	}
	return data, mimeType, nil
}

func validateGeneratedImageTaskBytes(body []byte) error {
	if len(body) > generatedImageTaskMaxBytes {
		return safeMessageError{message: "Generated image is too large"}
	}
	return nil
}

func normalizeGeneratedImageTaskMimeType(contentType string, source string, body []byte, fallbackMimeType string) string {
	for _, candidate := range []string{
		normalizeGeneratedImageTaskContentType(contentType),
		generatedImageTaskMimeTypeByExt(generatedImageTaskPathExt(source)),
		normalizeGeneratedImageTaskContentType(http.DetectContentType(body)),
		normalizeGeneratedImageTaskContentType(fallbackMimeType),
	} {
		if generatedImageTaskExtByMimeType(candidate) != "" {
			return candidate
		}
	}
	return ""
}

func normalizeGeneratedImageTaskContentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	switch value {
	case "image/jpg":
		return "image/jpeg"
	default:
		return value
	}
}

func generatedImageTaskExt(mimeType string, source string) string {
	if ext := generatedImageTaskExtByMimeType(mimeType); ext != "" {
		return ext
	}
	if ext := generatedImageTaskPathExt(source); ext != "" {
		return ext
	}
	return ".png"
}

func generatedImageTaskPathExt(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err == nil && parsed.Path != "" {
		if ext := strings.ToLower(pathpkg.Ext(parsed.Path)); ext != "" {
			return ext
		}
	}
	return strings.ToLower(filepath.Ext(value))
}

func generatedImageTaskExtByMimeType(mimeType string) string {
	switch normalizeGeneratedImageTaskContentType(mimeType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/gif":
		return ".gif"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	default:
		return ""
	}
}

func generatedImageTaskMimeTypeByExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".gif":
		return "image/gif"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	default:
		return ""
	}
}

func isGeneratedImageTaskURL(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), generatedImageTaskURLPrefix)
}

func generatedImageTaskDir() string {
	return filepath.Join(localMediaDataDir(), "generated-image-tasks")
}

func generatedImageTaskRecoveryPath(taskID string) string {
	return filepath.Join(generatedImageTaskRecoveryDir(), filepath.Base(taskID)+".json")
}

func generatedImageTaskRecoveryDir() string {
	return filepath.Join(localMediaDataDir(), "generated-image-task-recovery")
}

func writeGeneratedImageTaskFile(path string, body []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(perm); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errorsIsNotExist(removeErr) {
			return err
		}
		if retryErr := os.Rename(tmpPath, path); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func localMediaDataDir() string {
	driver := strings.ToLower(strings.TrimSpace(config.Cfg.StorageDriver))
	dsn := strings.TrimSpace(config.Cfg.DatabaseDSN)
	if (driver == "" || driver == "sqlite") && dsn != "" && dsn != ":memory:" && !strings.HasPrefix(dsn, "file:") {
		pathPart := dsn
		if index := strings.Index(dsn, "?"); index >= 0 {
			pathPart = dsn[:index]
		}
		if filepath.IsAbs(pathPart) {
			return filepath.Dir(pathPart)
		}
	}
	if _, err := os.Stat("/app/data"); err == nil {
		return "/app/data"
	}
	return "data"
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
