package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	tele "gopkg.in/telebot.v3"
)

const (
	def = iota
	img
	vid
	gif
	search
	nan
)

// DerpiResponse is what i need from derpibooru api
type DerpiResponse struct {
	SourceURL string

	ViewURL    string
	ThumbSmall string
}

func main() {
	domainName := os.Getenv("DOMAIN_NAME")
	logger := log.New(os.Stdout, "INFO: ", log.Lshortfile)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		logger.Printf("got signal: %v", <-ch)
		cancel()
	}()

	cache := NewCache(ctx)
	cs := NewServer(ctx, cache, domainName)

	//Start up bot
	pref := tele.Settings{
		Token:     os.Getenv("TOKEN"),
		Poller:    &tele.LongPoller{Timeout: 10 * time.Second},
		ParseMode: tele.ModeMarkdown,
		//Verbose:   true,
		//Synchronous: true,
	}
	logger.Printf("started bot with this token : %s", os.Getenv("TOKEN"))

	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
		return
	}
	//Inline link posting
	loaded := make(chan bool, 1)
	loaded <- true
	b.Handle(tele.OnQuery, func(c tele.Context) error {
		err := inlineQueryHandler(c, logger, loaded, cs)
		if err != nil {
			return err
		}
		return nil
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				b.Close()
				return
			}
		}
	}()

	b.Start()
}

func inlineQueryHandler(c tele.Context, logger *log.Logger, loaded chan bool, cs *CacheServer) error {
	//Check what are we dealing with
	format := checkType(c, logger)
	//Calculate offset for query
	var offset int64
	if c.Query().Offset != "" {
		var err error
		offset, err = strconv.ParseInt(c.Query().Offset, 10, 32)
		if err != nil {
			return err
		}
	} else {
		offset = 1
	}
	<-loaded
	//Deal with different types of metadata/searches
	switch format {
	case search:
		q := c.Query().Text
		q += "&page=" + fmt.Sprint(offset)
		results := searchQuery(q, logger, cs, false)
		logger.Printf("handling %s \n", c.Query().Text)
		c.Answer(&tele.QueryResponse{
			Results:    results,
			IsPersonal: true,
			CacheTime:  2 * 60,
			NextOffset: fmt.Sprint(offset + 1),
		})
		time.Sleep(time.Second * 3)
		loaded <- true
	case def:
		logger.Println("handling default")
		q := "safe%2C+first_seen_at.gt%3A1+days+ago%2C+-ai+generated&sf=wilson_score&sd=desc"
		//q := "safe%2C+first_seen_at.gt%3A1+days+ago%2C+-ai+generated%2C+score.gt%3A100"
		q += "&page=" + fmt.Sprint(offset)
		results := searchQuery(q, logger, cs, true)
		c.Answer(&tele.QueryResponse{
			Results:    results,
			IsPersonal: true,
			CacheTime:  2 * 60,
			NextOffset: fmt.Sprint(offset + 1),
		})
		time.Sleep(time.Second * 3)
		loaded <- true
	case img:
		results := getImage(c.Query().Text, logger, cs)
		c.Answer(&tele.QueryResponse{
			Results:    results,
			IsPersonal: false,
			CacheTime:  2 * 60,
		})
		//time.Sleep(time.Second * 3)
		loaded <- true
	}
	return nil
}

func checkType(c tele.Context, logger *log.Logger) int {
	format := nan

	if c.Query().Text == "" {
		format = def
		return format
	}

	u, err := url.Parse(c.Query().Text)
	if !(err == nil && u.Scheme != "" && u.Host != "") {
		logger.Printf("NOT URL: %s\n", c.Query().Text)
		format = search
		return format
	}

	postURL := c.Query().Text
	splittedURL := strings.Split(postURL, "/")
	postID := splittedURL[len(splittedURL)-1]

	//Here i do not do caching cause it does not contribute to API abuse
	//one-off operation almost certainly wont cause collision to justify cache use
	resp, err := http.Get("https://derpibooru.org/api/v1/json/images/" + postID)
	if err != nil {
		logger.Println(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Println(err)
	}
	mimeType := gjson.Get(string(body), "image.mime_type").Str
	smt := strings.Split(mimeType, "/")[0]
	switch smt {
	case "video":
		format = vid
	case "image":
		format = img
	}
	return format
}

func getImage(postURL string, logger *log.Logger, cs *CacheServer) tele.Results {
	splittedURL := strings.Split(postURL, "/")
	postID := splittedURL[len(splittedURL)-1]

	//Here i do not do caching cause it does not contribute to API abuse
	//one-off operation almost certainly wont cause collision to justify cache use
	resp, err := http.Get("https://derpibooru.org/api/v1/json/images/" + postID)
	if err != nil {
		logger.Println(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Println(err)
	}
	//------------------image caching--------------------

	thumb := gjson.Get(string(body), "image.representations.thumb_small").Str
	_, err = cs.cache.GetImageByURL(thumb)
	if err != nil {
		cs.cache.TMPSaveImage(thumb)
	}
	cacheThumbLinkID, err := GetImageID(thumb)
	if err != nil {
		logger.Println(err)
	}
	cacheThumbLink := cs.dn + cacheThumbLinkID

	//logger.Printf("\n--------------------------\n Added to cache link: %s\n replaced with: %s\n--------------------------\n", thumb, cacheThumbLink)

	//---------------------------------------------------
	derpResp := DerpiResponse{
		SourceURL:  gjson.Get(string(body), "image.source_url").Str,
		ViewURL:    gjson.Get(string(body), "image.representations.full").Str,
		ThumbSmall: cacheThumbLink,
	}
	aspectRatio := gjson.Get(string(body), "image.aspect_ratio").Num

	//Show result
	results := make(tele.Results, 1)
	result := &tele.PhotoResult{
		URL: derpResp.ViewURL,
		Caption: fmt.Sprintf("*Першоджерело*: %s\n*Derpibooru*: %s",
			formatURL(derpResp.SourceURL),
			postURL),
		ThumbURL: derpResp.ThumbSmall,
		Width:    int(100 * aspectRatio),
		Height:   100,
	}

	result.SetResultID(strconv.Itoa(1))
	results[0] = result
	return results
}

func searchQuery(query string, logger *log.Logger, cs *CacheServer, sfw bool) tele.Results {
	client := &http.Client{}

	q := "https://derpibooru.org/api/v1/json/search/images?"
	if !sfw {
		q = q + "filter_id=56027&" //everything
	}
	q = q + "q=" + query

	//------------------body caching--------------------

	_, err := cs.cache.GetBodyByURL(q)
	if err != nil {
		req, err := http.NewRequest("GET", q, nil)
		if err != nil {
			logger.Println(err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/115.0")
		req.Header.Set("Connection", "keep-alive")
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalln(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Println(err)
		}

		cs.cache.TMPSaveBody(q, b)
		logger.Println("SAVED BODY")
	}
	body, err := cs.cache.GetBodyByURL(q)
	if err != nil {
		logger.Println(err)
	}

	//--------------------------------------------------

	images := gjson.Get(string(body), "images").Array()
	results := make(tele.Results, len(images))
	for k, v := range images {
		//------------------image caching--------------------

		thumb := gjson.Get(string(body), "representations.thumb_small").Str
		_, err = cs.cache.GetImageByURL(thumb)
		if err != nil {
			cs.cache.TMPSaveImage(thumb)
			logger.Println("SAVED")
		}
		cacheThumbLinkID, err := GetImageID(thumb)
		if err != nil {
			logger.Println(err)
		}
		cacheThumbLink := cs.dn + cacheThumbLinkID

		//logger.Printf("\n--------------------------\n Added to cache link: %s\n replaced with: %s\n--------------------------\n", thumb, cacheThumbLink)

		//---------------------------------------------------
		derpResp := DerpiResponse{
			SourceURL:  gjson.Get(v.Raw, "source_url").Str,
			ViewURL:    gjson.Get(v.Raw, "representations.full").Str,
			ThumbSmall: cacheThumbLink,
		}
		aspectRatio := gjson.Get(v.Raw, "aspect_ratio").Num

		//Show result
		result := &tele.PhotoResult{
			URL: derpResp.ViewURL,
			Caption: fmt.Sprintf("*Першоджерело*: %s\n*Derpibooru*: %s",
				formatURL(derpResp.SourceURL),
				"https://derpibooru.org/images/"+strconv.Itoa(int(gjson.Get(v.Raw, "id").Int()))),
			ThumbURL: derpResp.ThumbSmall,
			Width:    int(100 * aspectRatio),
			Height:   100,
		}

		result.SetResultID(strconv.Itoa(k))
		results[k] = result

	}
	return results
}

// formatURL returns URL formatted with markdown for btter TG display
func formatURL(url string) string {
	//what if absent
	if url == "" {
		return "Немає :<"
	}
	//lim 37 , 3 dots, 34
	if len(url) > 35 {
		return fmt.Sprintf("[%s](%s)", string([]byte(url)[0:35])+"...", url)
	} else {
		return url
	}
}
