package main

import (
	"context"
	"image/jpeg"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

/*
	I pulled this away from cache so i do not feel overwhelmed
*/

type CacheInterface interface {
	TMPSaveImage(string) error
	GetImageByURL(string) (Image, error)
	GetImageByID(string) (Image, error)
	TMPSaveBody(string, []byte) error
	GetBodyByURL(string) ([]byte, error)
}

type CacheServer struct {
	dn     string
	cache  CacheInterface
	serv   *http.Server
	logger *log.Logger
}

func NewServer(ctx context.Context, c CacheInterface, dn string, logger *log.Logger) *CacheServer {
	is := CacheServer{}
	is.cache = c
	is.dn = dn
	is.logger = logger
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
func (is *CacheServer) GetImage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	img, err := is.cache.GetImageByID(id)
	if err != nil {
		http.Error(w, "Error getting image from cache", http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "image/jpeg")
	switch img.formatName {
	case "png", "jpeg":
		err = jpeg.Encode(w, img.img, nil)
		if err != nil {
			http.Error(w, "Error encoding image", http.StatusInternalServerError)
		}
	}

}
