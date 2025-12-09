package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
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
	fileNameString := base64.RawURLEncoding.EncodeToString(filenameBits) + "." + extString
	//upload to bucket
	putInput := &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileNameString,
		Body:        tempFile,
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
