package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
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

type Cahce struct {
	cache  *goc.Cache
	logger *log.Logger
}

type Image struct {
	img        image.Image
	formatName string
}

func NewCache(ctx context.Context, logger *log.Logger) *Cahce {
	c := goc.New(time.Hour*2, time.Minute*30)
	go func() {
		for {
			select {
			case <-ctx.Done():
				c.Flush()
				return
			}
		}
	}()
	return &Cahce{
		cache:  c,
		logger: logger,
	}
}

func (ic *Cahce) TMPSaveImage(derpiURL string) error {
	ic.logger.Printf("getting : %s\n", derpiURL)
	resp, err := http.Get(derpiURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	//----determine image type----
	_, formatName, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return err
	}
	var img image.Image
	switch formatName {
	case "png":
		img, err = png.Decode(bytes.NewReader(body))
		if err != nil {
			return err
		}
	case "jpeg":
		img, err = jpeg.Decode(bytes.NewReader(body))
		if err != nil {
			return err
		}
	case "gif":
		img, err = gif.Decode(bytes.NewReader(body))
		if err != nil {
			return err
		}
	}
	//----------------------------

	id, err := GetImageID(derpiURL)
	if err != nil {
		return err
	}
	ic.logger.Printf("saved thumb with id: %s\n", id)
	err = ic.cache.Add(id, Image{img, formatName}, time.Hour*2)
	if err != nil {
		return err
	}
	return nil
}

func (ic *Cahce) GetImageByURL(derpiURL string) (Image, error) {
	id, err := GetImageID(derpiURL)
	if err != nil {
		return Image{}, err
	}
	return ic.GetImageByID(id)
}

func (ic *Cahce) GetImageByID(id string) (Image, error) {
	img, ok := ic.cache.Get(id)
	if !ok {
		return Image{}, notHosted
	}
	val, ok := img.(Image)
	if !ok {
		return Image{}, badData
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

func (ic *Cahce) TMPSaveBody(derpiURL string, body []byte) error {
	ic.logger.Println("saved body")
	return ic.cache.Add(derpiURL, body, time.Hour*2)
}

func (ic *Cahce) GetBodyByURL(derpiURL string) ([]byte, error) {
	res, ok := ic.cache.Get(derpiURL)
	if !ok {
		return nil, notHosted
	}
	body, ok := res.([]byte)
	if !ok {
		return nil, badData
	}
	return body, nil
}
