package linkscraper

import (
	"fmt"
	"golang.org/x/net/html"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	httpTimeout  = time.Second * 3
	goroutines   = 20
	chanCapacity = 10 * 1000
)

var (
	NonExistingPage = &Page{
		Title: "404 NOT FOUND",
	}
)

type Page struct {
	URL   *url.URL `json:"-"`
	Title string
	Links []*Link
}

type Link struct {
	Title      string
	Page       *Page `json:"-"`
	TargetPage *Page `json:"-"`
	Target     string
	TargetURL  *url.URL `json:"-"`
	Internal   bool
	Anchor     bool
	Malformed  bool
	Dead       bool
}

type LinkChecker struct {
	client    *http.Client
	startURL  *url.URL
	visited   map[string]*Page
	visitLock sync.RWMutex
	queue     chan *Link
	waitGroup sync.WaitGroup

	StartPage *Page
}

func New(startURL string) (checker *LinkChecker, err error) {
	start, err := url.Parse(startURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing start URL: %v", err)
	}

	client := &http.Client{
		Timeout: httpTimeout,
	}

	resp, err := client.Get(startURL)
	if isTimeout(err) {
		return nil, fmt.Errorf("timeout while getting start URL: %v", err)
	} else if err != nil {
		return nil, fmt.Errorf("error getting start URL: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("unexpected status code for start URL: %d", resp.StatusCode)
	}

	return &LinkChecker{
		client:   client,
		startURL: start,
		visited:  make(map[string]*Page),
		queue:    make(chan *Link, chanCapacity),
	}, nil
}

func (l *LinkChecker) Visited() map[string]*Page {
	return l.visited
}

func (l *LinkChecker) Start() (err error) {
	l.waitGroup.Add(1)
	l.queue <- &Link{
		Dead:      false,
		Target:    l.startURL.String(),
		TargetURL: l.startURL,
		Malformed: false,
		Internal:  true,
		Anchor:    false,
	}

	for i := 0; i < goroutines; i++ {
		go l.runRoutine()
	}
	l.waitGroup.Wait()
	close(l.queue)

	l.StartPage = l.visited[l.startURL.String()]

	return nil
}

func (l *LinkChecker) runRoutine() {
	for {
		link, ok := <-l.queue
		if !ok {
			return // Were done!
		}
		l.processLink(link)

		l.waitGroup.Done()
	}
}

func (l *LinkChecker) processLink(link *Link) {
	l.visitLock.RLock()
	page := l.visited[link.TargetURL.String()]
	l.visitLock.RUnlock()

	if page != nil {
		link.TargetPage = page
		link.Dead = false
		return
	}

	statusCode, page, err := l.visitPage(link.TargetURL)
	if err != nil {
		log.Printf("Error visiting page: %s |> %v", link.Target, err)
		return
	}

	l.visitLock.Lock()
	l.visited[link.TargetURL.String()] = page
	l.visitLock.Unlock()

	link.TargetPage = page
	link.Dead = statusCode < 200 || statusCode > 299
}

// visitPage visits a link and gathers statistics for it (recursively)
func (l *LinkChecker) visitPage(curURL *url.URL) (statusCode int, page *Page, err error) {
	//log.Printf("Visiting URL: %s", curURL.String())

	resp, err := l.client.Get(curURL.String())
	if isTimeout(err) {
		return 0, NonExistingPage, nil

	}
	if err != nil {
		return 0, nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, NonExistingPage, nil
	}

	page = &Page{
		URL: curURL,
	}

	tokenizer := html.NewTokenizer(resp.Body)
	prevStartToken := tokenizer.Token()
	var lastLink *Link
	for {
		tokenType := tokenizer.Next()
		token := tokenizer.Token()
		switch tokenType {
		case html.ErrorToken:
			return //were done
		case html.StartTagToken:
			prevStartToken = token
			switch strings.ToLower(token.Data) {
			case "a":
				lastLink = l.processAnchor(page, token)
				if lastLink != nil {
					page.Links = append(page.Links, lastLink)
				}
			}
		case html.TextToken:
			switch strings.ToLower(prevStartToken.Data) {
			case "title":
				page.Title += strings.TrimSpace(token.String())
			case "a":
				if lastLink != nil {
					lastLink.Title += strings.TrimSpace(token.String())
				}
			}
		}
	}
	return resp.StatusCode, page, nil
}

func (l *LinkChecker) processAnchor(page *Page, anchor html.Token) *Link {
	for i := range anchor.Attr {
		if strings.ToLower(anchor.Attr[i].Key) != "href" {
			continue
		}

		link := &Link{
			Page:   page,
			Target: anchor.Attr[i].Val,
		}

		if strings.TrimSpace(anchor.Attr[i].Val) == "" {
			link.Malformed = true
			return link
		}
		if strings.HasPrefix(strings.TrimSpace(anchor.Attr[i].Val), "#") {
			link.Internal = true
			link.Anchor = true
			return link
		}
		var err error
		link.TargetURL, err = url.Parse(anchor.Attr[i].Val)
		if err != nil {
			link.Malformed = true
			log.Printf("Found an unparsable link: %s (%v)", anchor.Attr[i].Val, err)
			return link
		}

		link.Internal = l.isInternal(link.TargetURL)
		if link.Internal {
			l.waitGroup.Add(1)
			l.queue <- link
		}
		return link
	}
	return nil
}

func (l *LinkChecker) isInternal(URL *url.URL) bool {
	return l.startURL.Host == URL.Host
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout()
	}
	return false
}
