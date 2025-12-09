package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't parse form", err)
		return
	}
	file, header, err := r.FormFile("thumbnail")
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
	if contentType != "image/png" && contentType != "image/jpeg" {
		respondWithError(w, http.StatusBadRequest, "not correct file type", nil)
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
	fileNameString := base64.RawURLEncoding.EncodeToString(filenameBits)
	p := filepath.Join(cfg.assetsRoot, fileNameString+"."+extString)
	nf, err := os.Create(p)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to create new file", err)
		return
	}
	_, err = io.Copy(nf, file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to write to new file", err)
		return
	}

	metadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to read metadata from db", err)
		return
	}
	if metadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unable to read metadata from db", err)
		return
	}

	thumbnailstring := fmt.Sprintf("http://localhost:%s/assets/%s.%s", cfg.port, fileNameString, extString)
	metadata.ThumbnailURL = &thumbnailstring
	err = cfg.db.UpdateVideo(metadata)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to update metadata in db", err)
		return
	}

	respondWithJSON(w, http.StatusOK, metadata)
}
