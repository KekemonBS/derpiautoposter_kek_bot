package main

import (
	"context"
	"image"
	"image/png"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

/*
	I pulled this away from cache so i do not feel overwhelmed
*/

type ImageCacheInterface interface {
	TMPSave(string) error
	GetImageByURL(string) (image.Image, error)
	GetImageByID(string) (image.Image, error)
}

type ImageServer struct {
	dn    string
	cache ImageCacheInterface
	serv  *http.Server
}

func NewServer(ctx context.Context, ic ImageCacheInterface, dn string) *ImageServer {
	is := ImageServer{}
	is.cache = ic
	is.dn = dn
	//init server

	mux := mux.NewRouter()
	mux.HandleFunc("/{id:[0-9]+}", is.GetImage)
	s := &http.Server{
		Addr:           ":80",
		Handler:        mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	go func() {
		err := s.ListenAndServe()
		if err != nil {
			log.Fatal(err)
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				s.Close()
				return
			}
		}
	}()
	is.serv = s
	return &is
}

// it is simple as /<post_id>
func (is *ImageServer) GetImage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	w.Header().Set("Content-Type", "image/png")
	img, err := is.cache.GetImageByID(id)
	if err != nil {
		http.Error(w, "Error getting image from cache", http.StatusInternalServerError)
	}
	err = png.Encode(w, img)
	if err != nil {
		http.Error(w, "Error encoding image", http.StatusInternalServerError)
	}
}
