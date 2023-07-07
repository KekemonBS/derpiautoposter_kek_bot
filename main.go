package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
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
	logger := log.New(os.Stdout, "INFO: ", log.Lshortfile)

	//Start up bot
	pref := tele.Settings{
		Token:     os.Getenv("TOKEN"),
		Poller:    &tele.LongPoller{Timeout: 60 * time.Second},
		ParseMode: tele.ModeMarkdown,
	}
	logger.Printf("started bot with this token : %s", os.Getenv("TOKEN"))

	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
		return
	}
	//Inline link posting
	b.Handle(tele.OnQuery, func(c tele.Context) error {
		format := checkType(c, logger)
		switch format {
		case search:
			results := searchQuery(c.Query().Text, logger)
			logger.Printf("handling %s \n", c.Query().Text)
			c.Answer(&tele.QueryResponse{
				Results:    results,
				IsPersonal: true,
				CacheTime:  1,
			})
		case def:
			logger.Printf("handling %s \n", "default")
			results := searchQuery("safe, first_seen_at.gt:3 days ago, -ai generated&sf=wilson_score&sd=desc", logger)
			c.Answer(&tele.QueryResponse{
				Results:    results,
				IsPersonal: true,
				CacheTime:  1,
			})
		case img:
			postURL := c.Query().Text
			splittedURL := strings.Split(postURL, "/")
			postID := splittedURL[len(splittedURL)-1]
			resp, err := http.Get("https://derpibooru.org/api/v1/json/images/" + postID)
			if err != nil {
				logger.Println(err)
			}
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				logger.Println(err)
			}

			derpResp := DerpiResponse{
				SourceURL:  gjson.Get(string(body), "image.source_url").Str,
				ViewURL:    gjson.Get(string(body), "image.view_url").Str,
				ThumbSmall: gjson.Get(string(body), "image.representations.small").Str,
			}
			aspectRatio := gjson.Get(string(body), "image.aspect_ratio").Num

			//Show result
			results := make(tele.Results, 1)
			result := &tele.PhotoResult{
				URL: derpResp.ViewURL,
				Caption: fmt.Sprintf("*Першоджерело*: %s\n*Derpibooru*: %s",
					formatURL(derpResp.SourceURL),
					c.Query().Text),
				ThumbURL: derpResp.ThumbSmall,
				Width:    int(100 * aspectRatio),
				Height:   100,
			}

			result.SetResultID(strconv.Itoa(1))
			results[0] = result

			c.Answer(&tele.QueryResponse{
				Results:    results,
				IsPersonal: true,
				CacheTime:  1,
			})
		}
		return nil
	})
	b.Start()
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
	resp, err := http.Get("https://derpibooru.org/api/v1/json/images/" + postID)
	if err != nil {
		logger.Println(err)
	}
	body, err := ioutil.ReadAll(resp.Body)
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

func searchQuery(query string, logger *log.Logger) tele.Results {
	resp, err := http.Get("https://derpibooru.org/api/v1/json/search/images?filter_id=56027&q=" + query)
	if err != nil {
		logger.Println(err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logger.Println(err)
	}
	images := gjson.Get(string(body), "images").Array()
	results := make(tele.Results, len(images))
	for k, v := range images {
		derpResp := DerpiResponse{
			SourceURL:  gjson.Get(v.Raw, "source_url").Str,
			ViewURL:    gjson.Get(v.Raw, "view_url").Str,
			ThumbSmall: gjson.Get(v.Raw, "representations.small").Str,
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
	//lim 37 , 3 dots, 34
	if len(url) > 35 {
		return fmt.Sprintf("[%s](%s)", string([]byte(url)[0:35])+"...", url)
	} else {
		return url
	}
}
