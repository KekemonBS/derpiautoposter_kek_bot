package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"time"

	goc "github.com/patrickmn/go-cache"
)

/*
	Here i store incoming urls with expiration date in go-cache
	and start server so i can serve them via http. This makes bot
	cache data to not get rate limited
*/

var notHosted error = errors.New("Not hosted")
var badData error = errors.New("Bad data")

type ImageCache struct {
	cache *goc.Cache
}

func NewCache(ctx context.Context) *ImageCache {
	c := goc.New(time.Second*15, time.Minute*2)
	go func() {
		for {
			select {
			case <-ctx.Done():
				c.Flush()
				return
			}
		}
	}()
	return &ImageCache{
		cache: c,
	}
}

func (ic *ImageCache) TMPSave(derpiURL string) error {
	resp, err := http.Get(derpiURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	img, err := png.Decode(resp.Body)
	if err != nil {
		return err
	}
	id, err := GetImageID(derpiURL)
	if err != nil {
		return err
	}
	fmt.Printf("saved thumbnail in cache with id: %s\n", id)
	err = ic.cache.Add(id, img, time.Minute*15)
	if err != nil {
		return err
	}
	return nil
}

func (ic *ImageCache) GetImageByURL(derpiURL string) (image.Image, error) {
	id, err := GetImageID(derpiURL)
	if err != nil {
		return nil, err
	}
	return ic.GetImageByID(id)
}

func (ic *ImageCache) GetImageByID(id string) (image.Image, error) {
	img, ok := ic.cache.Get(id)
	if !ok {
		return nil, notHosted
	}
	val, ok := img.(image.Image)
	if !ok {
		return nil, badData
	}
	return val, nil
}

func GetImageID(derpiThumbURL string) (string, error) {
	u, err := url.Parse(derpiThumbURL)
	if err != nil || u.Path == "" || u.Path == "/" {
		return "", err
	}
	return getURLSegments(u.Path)[5], nil
}

func getURLSegments(path string) []string {
	unescaped, err := url.PathUnescape(path)
	if err != nil {
		return []string{}
	}
	return strings.Split(unescaped, "/")
}
