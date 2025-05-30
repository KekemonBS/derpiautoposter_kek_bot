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
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	tele "gopkg.in/telebot.v4"
)

const (
	def = iota
	media
	search
	nan
)

// DerpiResponse is what i need from derpibooru api
type DerpiResponse struct {
	SourceURL string

	ViewURL    string
	ThumbSmall string
}

type debouncer struct {
	mu          *sync.Mutex
	timers      map[int64]*time.Timer
	lastChannel map[int64]chan bool
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

	cache := NewCache(ctx, logger)
	cs := NewServer(ctx, cache, domainName, logger)

	//Create poller
	poller := &tele.MiddlewarePoller{
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},

		Filter: func(_ *tele.Update) bool {
			return true
		},
	}
	//Setup preferences
	pref := tele.Settings{
		Token:     os.Getenv("TOKEN"),
		Poller:    poller,
		ParseMode: tele.ModeMarkdown,
		//Verbose:   true,
		//Synchronous: true,
	}
	logger.Printf("started bot with this token : %s", os.Getenv("TOKEN"))

	//Start up bot
	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
		return
	}
	//Inline link posting
	//loaded := make(chan bool, 1)
	//loaded <- true

	d := &debouncer{
		mu:          &sync.Mutex{},
		timers:      make(map[int64]*time.Timer),
		lastChannel: make(map[int64]chan bool),
	}

	b.Handle(tele.OnQuery, func(c tele.Context) error {
		err := inlineQueryDebouncer(c, logger, cs, d)
		if err != nil {
			return err
		}
		return nil
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				_, err = b.Close()
				if err != nil {
					log.Fatal(err)
				}
				return
			}
		}
	}()

	b.Start()
}

// inlineQueryDebouncer discards updates that were sent in the last 2 second (wait till user stops typing)
func inlineQueryDebouncer(c tele.Context, logger *log.Logger, cs *CacheServer, d *debouncer) error {

	//To make sure default queries, links and cached queries sent faster
	format := checkSearchType(c)
	_, err := cs.cache.GetBodyByURL(c.Query().Text)
	if format == def || format == media || err != nil {
		time.Sleep(200 * time.Millisecond)
		err := inlineQueryHandler(c, logger, cs)
		if err != nil {
			return err
		}
	}

	u := c.Update()

	errChan := make(chan error)
	defer close(errChan)
	timers := d.timers
	lastChannel := d.lastChannel
	go func(u tele.Update) {
		d.mu.Lock()
		// Create a new timer if none exists and select it
		var timer *time.Timer
		if _, ok := timers[u.Query.Sender.ID]; !ok {
			timer = time.NewTimer(1500 * time.Millisecond)
			timers[u.Query.Sender.ID] = timer
		} else {
			timer = timers[u.Query.Sender.ID]
		}

		//Every new query resets the time
		if u.Query.Offset > "0" {
			timer.Reset(200 * time.Millisecond) //consequent is faster
		} else {
			timer.Reset(1500 * time.Millisecond)
		}

		//Every query discards the last query
		if _, ok := lastChannel[u.Query.Sender.ID]; !ok {
			lastChannel[u.Query.Sender.ID] = make(chan bool)
		} else {
			lastChannel[u.Query.Sender.ID] <- true
		}

		//And places own channel for possible discarding by new query
		ownChannel := make(chan bool)
		lastChannel[u.Query.Sender.ID] = ownChannel

		d.mu.Unlock()
		select {
		//Wait to be discarded by next query
		case <-ownChannel:
			logger.Printf("discarding query: %s", c.Query().Text)
			return
		//Or wait on timer to be processed
		case <-timer.C:
			logger.Printf("accepting query: %s", c.Query().Text)
			d.mu.Lock()
			delete(timers, u.Query.Sender.ID)
			delete(lastChannel, u.Query.Sender.ID)
			d.mu.Unlock()
			err := inlineQueryHandler(c, logger, cs)
			if err != nil {
				errChan <- err
			}
			return
		}
	}(u)

	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
}

func inlineQueryHandler(c tele.Context, logger *log.Logger, cs *CacheServer) error {
	//Check what are we dealing with
	format := checkSearchType(c)
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
	//<-loaded
	//Deal with different types of metadata/searches
	var err error
	switch format {
	case search:
		q := c.Query().Text
		q += "&page=" + fmt.Sprint(offset)
		results := searchQuery(q, logger, cs, false)
		logger.Printf("handling %s \n", c.Query().Text)
		err = c.Answer(&tele.QueryResponse{
			Results:    results,
			IsPersonal: true,
			CacheTime:  0,
			NextOffset: fmt.Sprint(offset + 1),
		})
		//loaded <- true
	case def:
		logger.Println("handling default")
		q := os.Getenv("DEFAULT_QUERY")
		//q := "safe%2C+first_seen_at.gt%3A1+days+ago%2C+-ai+generated&sf=wilson_score&sd=desc"
		//q := "safe%2C+first_seen_at.gt%3A1+days+ago%2C+-ai+generated%2C+score.gt%3A100"
		q += "&page=" + fmt.Sprint(offset)
		results := searchQuery(q, logger, cs, true)
		err = c.Answer(&tele.QueryResponse{
			Results:    results,
			IsPersonal: true,
			CacheTime:  0,
			NextOffset: fmt.Sprint(offset + 1),
		})
		//loaded <- true
	case media:
		results := getMedia(c.Query().Text, logger, cs)
		err = c.Answer(&tele.QueryResponse{
			Results:    results,
			IsPersonal: false,
			CacheTime:  0,
		})
		//loaded <- true
	}
	return err
}

func checkSearchType(c tele.Context) int {
	if c.Query().Text == "" {
		return def
	}

	u, err := url.Parse(c.Query().Text)
	if !(err == nil && u.Scheme != "" && u.Host != "") {
		return search
	}

	return media
}

func getMedia(postURL string, logger *log.Logger, cs *CacheServer) tele.Results {
	u, _ := url.Parse(postURL)
	splittedURL := strings.Split(postURL, "/")
	postID := splittedURL[len(splittedURL)-1]

	//Here i do not do caching cause it does not contribute to API abuse
	//one-off operation almost certainly wont cause collision to justify cache use
	resp, err := http.Get("https://" + u.Host + "/api/v1/json/images/" + postID)
	if err != nil {
		logger.Println(err)
	}
	defer func() {
		err = resp.Body.Close()
		if err != nil {
			logger.Println("failed during body close")
			return
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Println(err)
	}

	//Skip video caching
	results := make(tele.Results, 1)
	mimeType := gjson.Get(string(body), "image.mime_type").Str
	switch mimeType {
	case "video/webm":
		//Telegram does not support inline video results
		results[0] = &tele.ArticleResult{
			Title: "Videos are not supported",
			Text:  "Videos are not supported",
		}
		return results
	}

	//Get image dimensions
	width := gjson.Get(string(body), "image.width").Int()
	height := gjson.Get(string(body), "image.height").Int()

	//yet i stil cache to make use of local image lookup
	//------------------image caching--------------------
	jsonString := gjson.Get(string(body), "image").String() //{"image":{get this}}
	cacheThumbLink := cacheImage(cs, logger, jsonString, width, height, u)
	logger.Println(cacheThumbLink)
	//---------------------------------------------------

	//------------------put thumbnail directly bypassing caching--------------------
	var thumb string
	// Check if image thumbnail is not too small for telegram
	if width > 3000 || height > 3000 {
		thumb = gjson.Get(jsonString, "representations.thumb_small").Str
	} else {
		thumb = gjson.Get(jsonString, "representations.thumb").Str
	}
	//ponerpics specific fix, might work with others
	nu, _ := url.Parse(thumb)
	if nu.Host == "" {
		thumb = u.Scheme + "://" + u.Host + thumb
	}
	//-----------------------------------------------------------------------------

	//Check if image is not too large for telegram
	var viewURL string
	if width > 2000 || height > 2000 {
		viewURL = gjson.Get(string(body), "image.representations.medium").Str
	} else {
		viewURL = gjson.Get(string(body), "image.representations.full").Str
	}

	//ponerpics specific fix, might work with others
	nu, _ = url.Parse(viewURL)
	if nu.Host == "" {
		viewURL = u.Scheme + "://" + u.Host + viewURL
	}

	derpResp := DerpiResponse{
		SourceURL: gjson.Get(string(body), "image.source_url").Str,
		ViewURL:   viewURL,
		//ThumbSmall: cacheThumbLink,
		ThumbSmall: thumb,
	}

	//prone to malfunction consider replacing
	split := strings.Split(u.Host, ".")
	var serviceName string
	if split[0] == "www" {
		serviceName = split[1]
	} else {
		serviceName = split[0]
	}

	//Show result
	switch mimeType {
	case "image/gif":
		result := &tele.GifResult{
			URL: derpResp.ViewURL,
			Caption: fmt.Sprintf("*Першоджерело*: %s\n*%s*: %s",
				formatURL(derpResp.SourceURL),
				cases.Title(language.English).String(serviceName),
				stripPostURL(postURL)),
			ThumbURL: derpResp.ThumbSmall,
			Cache:    "",
		}
		result.SetResultID(strconv.Itoa(1))
		results[0] = result
	default:
		result := &tele.PhotoResult{
			URL: derpResp.ViewURL,
			Caption: fmt.Sprintf("*Першоджерело*: %s\n*%s*: %s",
				formatURL(derpResp.SourceURL),
				cases.Title(language.English).String(serviceName),
				stripPostURL(postURL)),
			ThumbURL: derpResp.ThumbSmall,
			Cache:    "",
		}
		result.SetResultID(strconv.Itoa(1))
		results[0] = result
	}
	return results
}

// Telegam forbids mixing gifs and images so this function remains intact
func searchQuery(query string, logger *log.Logger, cs *CacheServer, sfw bool) tele.Results {
	q := "https://derpibooru.org/api/v1/json/search/images?"
	if !sfw {
		q = q + "filter_id=100073&" //56027&" //everything
	} else {
		q = q + "filter_id=100073&" //default
	}
	q = q + "q=" + query
	u, _ := url.Parse(q)
	//------------------body caching--------------------
	body := cacheBody(cs, logger, q)
	//--------------------------------------------------

	images := gjson.Get(string(body), "images").Array()
	var results tele.Results
	var skip int
	for k, v := range images {
		//Skip video caching
		mimeType := gjson.Get(v.String(), "mime_type").Str
		switch mimeType {
		case "video/webm":
			//Telegram does not support inline video results
			skip++
			continue
		}

		//Get image dimensions
		width := gjson.Get(v.String(), "width").Int()
		height := gjson.Get(v.String(), "height").Int()

		//------------------image caching--------------------
		cacheThumbLink := cacheImage(cs, logger, v.String(), width, height, u)
		//---------------------------------------------------

		//Check if image is not too large for telegram
		var viewURL string
		if width > 2000 || height > 2000 {
			viewURL = gjson.Get(v.String(), "representations.medium").Str
		} else {
			viewURL = gjson.Get(v.String(), "representations.full").Str
		}

		derpResp := DerpiResponse{
			SourceURL:  gjson.Get(v.Raw, "source_url").Str,
			ViewURL:    viewURL,
			ThumbSmall: cacheThumbLink,
		}

		//Show result
		result := &tele.PhotoResult{
			URL: derpResp.ViewURL,
			Caption: fmt.Sprintf("*Першоджерело*: %s\n*Derpibooru*: %s",
				formatURL(derpResp.SourceURL),
				"https://derpibooru.org/images/"+strconv.Itoa(int(gjson.Get(v.Raw, "id").Int()))),
			ThumbURL: derpResp.ThumbSmall,
			Cache:    "",
		}

		result.SetResultID(strconv.Itoa(k - skip))
		results = append(results, result)

	}
	return results
}

// cacheImage selects small thumbnail from provided json,
// if havent cached yet, saves it (cache key ID) and returns link to saved file
func cacheImage(cs *CacheServer, logger *log.Logger, jsonString string, width int64, height int64, u *url.URL) string {
	var thumb string
	//Check if image thumbnail is not too small for telegram
	if width > 3000 || height > 3000 {
		thumb = gjson.Get(jsonString, "representations.thumb_small").Str
	} else {
		thumb = gjson.Get(jsonString, "representations.thumb").Str
	}

	//ponerpics specific fix, might work with others
	nu, _ := url.Parse(thumb)
	if nu.Host == "" {
		thumb = u.Scheme + "://" + u.Host + thumb
	}

	_, err := cs.cache.GetImageByURL(thumb)
	if err != nil {
		err = cs.cache.TMPSaveImage(thumb)
		if err != nil {
			logger.Println(err)
		}
	}
	cacheThumbLinkID, err := GetImageID(thumb)
	if err != nil {
		logger.Println(err)
	}
	cacheThumbLink := cs.dn + cacheThumbLinkID
	//logger.Printf("\n--------------------------\n Added to cache link: %s\n replaced with: %s\n--------------------------\n", thumb, cacheThumbLink)
	return cacheThumbLink
}

// cacheBody saves body in cahce (cache key query) and if its absent and returns if from cache
func cacheBody(cs *CacheServer, logger *log.Logger, q string) []byte {
	client := &http.Client{}
	_, err := cs.cache.GetBodyByURL(q)
	if err != nil {
		req, err := http.NewRequest("GET", q, nil)
		if err != nil {
			logger.Println(err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/117.0")
		req.Header.Set("Connection", "keep-alive")
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalln(err)
		}
		defer func() {
			err = resp.Body.Close()
			if err != nil {
				logger.Println("failed during body close")
				return
			}
		}()

		b, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Println(err)
		}

		err = cs.cache.TMPSaveBody(q, b)
		if err != nil {
			log.Fatal(err)
		}
	}
	body, err := cs.cache.GetBodyByURL(q)
	if err != nil {
		logger.Println(err)
	}
	return body
}

// formatURL returns URL formatted with markdown for btter TG display
func formatURL(URL string) string {
	//what if absent
	if URL == "" {
		return "Немає :<"
	}
	//lim 37 , 3 dots, 34
	if len(URL) > 35 {
		return fmt.Sprintf("[%s](%s)", string([]byte(URL)[0:35])+"...", URL)
	}
	return URL
}

// stripPostURL trips search query from post url
func stripPostURL(URL string) string {
	u, err := url.Parse(URL)
	if err != nil {
		return URL
	}
	return u.Scheme + "://" + u.Host + u.Path
}
