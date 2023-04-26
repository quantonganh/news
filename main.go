package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/google/go-querystring/query"
)

const (
	baseURL        = "https://vnexpress.net"
	getCommentsURL = "https://usi-saas.vnexpress.net/index/get"
)

type Category struct {
	Link string
	ID   string
}

type Article struct {
	URL   string    `json:"url"`
	Title string    `json:"title"`
	Date  time.Time `json:"time""`
	Likes int       `json:"likes"`
}

type BoxComment struct {
	ArticleId   string `json:"article_id" url:"objectid"`
	ArticleType string `json:"article_type" url:"objecttype"`
	SiteId      string `json:"site_id" url:"siteid"`
	CategoryId  string `json:"category_id" url:"catetoryid"`
	Sign        string `json:"sign" url:"sign"`
	Limit       int    `json:"limit" url:"limit"`
	TabActive   string `json:"tab_active" url:"tab_active"`
}

type Comment struct {
	Error            int    `json:"error"`
	ErrorDescription string `json:"errorDescription"`
	Iscomment        int    `json:"iscomment"`
	Data             struct {
		Total     int `json:"total"`
		Totalitem int `json:"totalitem"`
		Items     []struct {
			CommentId    string `json:"comment_id"`
			ParentId     string `json:"parent_id"`
			ArticleId    int    `json:"article_id"`
			Content      string `json:"content"`
			FullName     string `json:"full_name"`
			CreationTime int    `json:"creation_time"`
			Time         string `json:"time"`
			Userlike     int    `json:"userlike"`
			TR1          int    `json:"t_r_1"`
			TR2          int    `json:"t_r_2"`
			TR3          int    `json:"t_r_3"`
			TR4          int    `json:"t_r_4"`
			Replys       struct {
				Total int           `json:"total,omitempty"`
				Items []interface{} `json:"items,omitempty"`
			} `json:"replys"`
			Userid       *int `json:"userid"`
			Type         int  `json:"type"`
			LikeIsmember bool `json:"like_ismember"`
			Rating       struct {
			} `json:"rating"`
			IsPin int `json:"is_pin"`
		} `json:"items"`
		ItemsPin []interface{} `json:"items_pin"`
		Offset   int           `json:"offset"`
	} `json:"data"`
	Csrf string `json:"_csrf"`
}

func main() {
	c := colly.NewCollector(
		colly.Async(true),
		colly.AllowedDomains("vnexpress.net"),
		colly.MaxDepth(3),
	)

	c.Limit(&colly.LimitRule{
		Parallelism: runtime.NumCPU(),
	})

	articleCollector := c.Clone()

	toDate := time.Now()
	fromDate := toDate.AddDate(0, 0, -7)
	c.OnHTML("#wrap-main-nav > nav > ul > li", func(e *colly.HTMLElement) {
		id := e.Attr("data-id")
		link := e.ChildAttr("a", "href")
		if id != "" && strings.HasPrefix(link, "/") && link != "/" {
			// https://vnexpress.net/category/day?cateid=1001005&fromdate=1682121600&todate=1682726400&allcate=1001005
			categoryURL := fmt.Sprintf("%s/category/day?cateid=%s&fromdate=%d&todate=%d&allcate=%s", baseURL, id, fromDate.Unix(), toDate.Unix(), id)
			e.Request.Visit(categoryURL)
		}
	})

	c.OnHTML("#pagination .button-page a[href]", func(e *colly.HTMLElement) {
		e.Request.Visit(e.Attr("href"))
	})

	c.OnRequest(func(req *colly.Request) {
		fmt.Println("Visiting", req.URL)
	})

	c.OnHTML(".item-news .title-news a[href]", func(e *colly.HTMLElement) {
		articleCollector.Visit(e.Attr("href"))
	})

	articleCollector.OnRequest(func(req *colly.Request) {
		fmt.Println("Visiting", req.URL)
	})

	articleCh := make(chan Article)
	articleCollector.OnHTML("body", func(e *colly.HTMLElement) {
		title := e.ChildText(".top-detail .container .sidebar-1 .title-detail")
		article := Article{
			URL:   e.Request.URL.String(),
			Title: title,
		}

		dataComponentInput := e.ChildAttr("#box_comment_vne", "data-component-input")
		totalLikes, err := countTotalLikes(dataComponentInput)
		if err == nil && totalLikes > 0 {
			article.Likes = totalLikes
		}

		date := e.ChildText(".top-detail .container .sidebar-1 .header-content span.date")
		article.Date = parseDate(date)
		articleCh <- article
	})

	c.Visit(baseURL)

	go func() {
		c.Wait()
		articleCollector.Wait()
		close(articleCh)
	}()

	articles := make([]Article, 0)
	for article := range articleCh {
		articles = append(articles, article)
	}
	sort.Slice(articles, func(i, j int) bool {
		return articles[i].Likes > articles[j].Likes
	})

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	n := len(articles)
	if n >= 10 {
		enc.Encode(articles[:10])
	} else {
		enc.Encode(articles[:n])
	}
}

func parseDate(s string) time.Time {
	layouts := []string{"02/01/2006 15:04", "02/1/2006 15:04", "2/1/2006 15:04"}
	re := regexp.MustCompile(`(\d{1,2}/\d{1,2}/\d{4}),\s(\d{2}:\d{2})`)
	match := re.FindStringSubmatch(s)
	if match != nil {
		for _, layout := range layouts {
			d, err := time.Parse(layout, fmt.Sprintf("%s %s", match[1], match[2]))
			if err == nil {
				return d
			}
		}
	}
	return time.Time{}
}

func countTotalLikes(dataComponentInput string) (int, error) {
	var boxComment *BoxComment
	if err := json.Unmarshal([]byte(dataComponentInput), &boxComment); err != nil {
		return 0, err
	}
	v, _ := query.Values(boxComment)

	req, err := http.NewRequest(http.MethodGet, getCommentsURL, nil)
	if err != nil {
		return 0, err
	}
	req.URL.RawQuery = v.Encode()

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var comment *Comment
	if err := json.NewDecoder(resp.Body).Decode(&comment); err != nil {
		return 0, err
	}

	totalLikes := 0
	for _, item := range comment.Data.Items {
		totalLikes += item.Userlike
	}

	return totalLikes, nil
}
