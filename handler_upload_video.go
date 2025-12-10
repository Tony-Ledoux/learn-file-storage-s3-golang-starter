package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	metadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to read metadata from db", err)
		return
	}

	if metadata.UserID != userID {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()
	contentType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse content type", err)
		return
	}
	if contentType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "not an mp4 file", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to create temorary file", err)
		return
	}
	defer tempFile.Close()
	defer os.Remove(tempFile.Name())
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to copy into temorary file", err)
		return
	}
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to reset pointer to start of temorary file", err)
		return
	}
	mediaType := strings.Split(contentType, "/")
	extString := mediaType[len(mediaType)-1]
	filenameBits := make([]byte, 32)
	_, err = rand.Read(filenameBits)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to generate random bits", err)
		return
	}
	//upload to bucket
	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to determine aspect ratio", err)
		return
	}
	// Process video for fast start
	processedPath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to process video", err)
		return
	}
	defer os.Remove(processedPath)

	// Open the processed file for upload
	processedFile, err := os.Open(processedPath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to open processed file", err)
		return
	}
	defer processedFile.Close()

	var prefix string
	switch aspectRatio {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	default:
		prefix = "other"
	}
	fileNameString := prefix + "/" + base64.RawURLEncoding.EncodeToString(filenameBits) + "." + extString

	putInput := &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileNameString,
		Body:        processedFile,
		ContentType: &contentType,
	}
	_, err = cfg.s3Client.PutObject(r.Context(), putInput)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to upload", err)
		return
	}

	S3Path := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileNameString)
	metadata.VideoURL = &S3Path
	err = cfg.db.UpdateVideo(metadata)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to update metadata in db", err)
		return
	}

	respondWithJSON(w, http.StatusOK, metadata)
}

func getVideoAspectRatio(filePath string) (string, error) {
	type Stream struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	type ProbeData struct {
		Streams []Stream `json:"streams"`
	}
	var buffer bytes.Buffer
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	cmd.Stdout = &buffer
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	var probeData ProbeData
	err = json.Unmarshal(buffer.Bytes(), &probeData)
	if err != nil {
		return "", err
	}
	if len(probeData.Streams) == 0 {
		return "", fmt.Errorf("no streams found in video")
	}

	// Get the first video stream
	width := probeData.Streams[0].Width
	height := probeData.Streams[0].Height

	if width == 0 || height == 0 {
		return "", fmt.Errorf("invalid dimensions")
	}

	// Calculate aspect ratio using integer division
	ratio := float64(width) / float64(height)

	// 16:9 = 1.777..., 9:16 = 0.5625
	// Using tolerance of 0.1
	if ratio > 1.6 && ratio < 1.9 {
		return "16:9", nil
	} else if ratio > 0.5 && ratio < 0.65 {
		return "9:16", nil
	}

	return "other", nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outputPath := filePath + ".processing"

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputPath)

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	return outputPath, nil
}
