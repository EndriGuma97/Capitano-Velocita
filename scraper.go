package main

import (
	"encoding/csv"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/tebeka/selenium"
	"github.com/tebeka/selenium/chrome"
)

var tpl = template.Must(template.ParseFiles("index.html"))
var data []scrapeResult
var dataMutex sync.Mutex // To handle concurrent writes to the global data slice

var clients = make(map[chan string]bool)
var clientsMutex sync.Mutex

type scrapeResult struct {
	CompanyName   string
	PhoneNumber   string
	WebsiteLink   string
	CompanyType   string
	Email         string
	InstagramLink string
	LinkedInLink  string
	DirectionsLink string
}

func main() {
	http.HandleFunc("/", serveIndex)
	http.HandleFunc("/search", handleSearch)
	http.HandleFunc("/download", handleDownload)
	http.HandleFunc("/events", handleEvents)
	log.Println("Starting server on :8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	tpl.Execute(w, nil)
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	searchQuery := r.FormValue("query")
	searchQuery = url.QueryEscape(searchQuery) // Encode spaces as %20

	// Start scraping process
	go startScraping(searchQuery)

	// No need to write any response, as the page should not change
}

func startScraping(searchQuery string) {
	const chromeDriverPath = "C:/Users/user/OneDrive/Desktop/chromedriver-win64/chromedriver" // update this path if necessary

	service, err := selenium.NewChromeDriverService(chromeDriverPath, 4444)
	if err != nil {
		log.Fatal("Error starting the ChromeDriver server:", err)
	}
	defer service.Stop()

	caps := selenium.Capabilities{}
	chromeCaps := chrome.Capabilities{
		Args: []string{"--headless"},
	}
	caps.AddChrome(chromeCaps)

	driver, err := selenium.NewRemote(caps, "")
	if err != nil {
		log.Fatal("Error creating new WebDriver session:", err)
	}
	defer driver.Quit()

	baseURL := "https://www.google.com/localservices/prolist?g2lbs=AIQllVxIHOWB2FeYgHr2lKln8lg04OMkqPj5SNnPXLWWA9EFJayNguM4iiWDR3qgSPtlui5NHLmRxmN-BfoFiY9MdJmjWI5vcICS61nQQDDBnSM2Kdv8DzteKsW9QZdIOeB2p3pm1J4m&hl=en-AL&gl=al&ssta=1&oq=&src=2&sa=X&scp=CgASABoAKgA%3D&q=" + searchQuery + "&ved=0CAUQjdcJahgKEwiw6c7CqJWGAxUAAAAAHQAAAAAQvwE&slp=MgBAAVIECAIgAIgBAJoBBgoCFxkQAA%3D%3D"

	/*IMPORTANT*/
	//lciValues := []string{"", "20", "40", "60", "80", "100", "120", "140", "160", "180"} // Add more values if needed
	lciValues := []string{""} // Add more values if needed

	for _, lci := range lciValues {
		searchURL := baseURL
		if lci != "" {
			searchURL += "&lci=" + lci
		}
		scrapeURL(driver, searchURL)
	}

	// Notify all clients that the scraping is done
	notifyClients("Information has been downloaded")
}

func scrapeURL(driver selenium.WebDriver, searchURL string) {
	driver.Get(searchURL)
	driver.MaximizeWindow("")

	firstButton := true

	for i := 0; i < 21; i++ {
		pageElements, err := driver.FindElements(selenium.ByXPATH, `/html/body/c-wiz/div/div[3]/div/div/div[1]/div[3]/div[3]/c-wiz/div/div/div[1]/c-wiz/div`)
		if err != nil {
			log.Println("Error finding elements:", err)
			continue
		}

		if len(pageElements) == 0 {
			break // Exit the loop if no elements are found
		}

		textContent, err := pageElements[0].GetAttribute("innerHTML")
		if err != nil {
			log.Println("Error getting innerHTML:", err)
			continue
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(textContent))
		if err != nil {
			log.Println("Error parsing HTML:", err)
			continue
		}

		doc.Find("div[data-test-id='organic-list-card']").Each(func(_ int, s *goquery.Selection) {
			companyName := s.Find("div.rgnuSb.xYjf2e").Text()
			phoneNumber, _ := s.Find("a[data-phone-number]").Attr("data-phone-number")
			websiteLink, _ := s.Find("a[aria-label='Website']").Attr("href")
			companyType := s.Find("span.hGz87c").Text()
			directionsLink, _ := s.Find("a[aria-label='Directions']").Attr("href")

			email, instagram, linkedin := "", "", ""
			if websiteLink != "" {
				email, instagram, linkedin = extractContactsFromWebsite(driver, websiteLink)
			}

			result := scrapeResult{
				CompanyName:   companyName,
				PhoneNumber:   phoneNumber,
				WebsiteLink:   websiteLink,
				CompanyType:   companyType,
				Email:         email,
				InstagramLink: instagram,
				LinkedInLink:  linkedin,
				DirectionsLink: directionsLink,
			}

			dataMutex.Lock()
			data = append(data, result)
			dataMutex.Unlock()
		})

		time.Sleep(2 * time.Second)
		var nextPageButtonXPath string
		if firstButton {
			nextPageButtonXPath = `/html/body/c-wiz/div/div[3]/div/div/div[1]/div[3]/div[3]/c-wiz/div/div/div[2]/div/div/button/span`
			firstButton = false
		} else {
			nextPageButtonXPath = `/html/body/c-wiz/div/div[3]/div/div/div[1]/div[3]/div[3]/c-wiz/div/div/div[2]/div[2]/div/button/span`
		}

		nextButton, err := driver.FindElement(selenium.ByXPATH, nextPageButtonXPath)
		if err != nil {
			log.Println("Error finding next page button:", err)
			break
		}

		if err := nextButton.Click(); err != nil {
			log.Println("Error clicking next page button:", err)
			break
		}
	}
}

func extractContactsFromWebsite(driver selenium.WebDriver, url string) (string, string, string) {
	email, instagram, linkedin := "", "", ""
	err := driver.Get(url)
	if err != nil {
		log.Println("Error loading URL:", err)
		return "", "", ""
	}

	time.Sleep(2 * time.Second)

	pageSource, err := driver.PageSource()
	if err != nil {
		log.Println("Error getting page source:", err)
		return "", "", ""
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageSource))
	if err != nil {
		log.Println("Error parsing page source:", err)
		return "", "", ""
	}

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		if strings.Contains(href, "mailto:") {
			email = strings.TrimPrefix(href, "mailto:")
		} else if strings.Contains(href, "instagram.com") {
			instagram = href
		} else if strings.Contains(href, "linkedin.com") {
			linkedin = href
		}
	})

	if email == "" {
		emailRegex := regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,7}`)
		emailMatch := emailRegex.FindString(doc.Text())
		if emailMatch != "" {
			email = emailMatch
		}
	}

	return email, instagram, linkedin
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	dataMutex.Lock()
	defer dataMutex.Unlock()

	if len(data) == 0 {
		http.Error(w, "No data available", http.StatusNoContent)
		return
	}

	// Set headers to indicate a file attachment with the correct filename
	w.Header().Set("Content-Disposition", "attachment; filename=output.csv")
	w.Header().Set("Content-Type", "text/csv")

	// Create a CSV writer to write directly to the response writer
	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Write header
	writer.Write([]string{"Company Name", "Phone Number", "Email", "Instagram Link", "LinkedIn Link", "Website Link", "Company Type", "Directions Link"})

	// Write data
	for _, record := range data {
		writer.Write([]string{
			record.CompanyName,
			record.PhoneNumber,
			record.Email,
			record.InstagramLink,
			record.LinkedInLink,
			record.WebsiteLink,
			record.CompanyType,
			record.DirectionsLink,
		})
	}
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// Set the headers related to event streaming.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Create a channel for this client and add it to the map
	messageChan := make(chan string)
	clientsMutex.Lock()
	clients[messageChan] = true
	clientsMutex.Unlock()

	// Ensure that when we finish, we remove the client's channel
	defer func() {
		clientsMutex.Lock()
		delete(clients, messageChan)
		clientsMutex.Unlock()
		close(messageChan)
	}()

	// Listen to the client's connection until it is closed
	notify := w.(http.CloseNotifier).CloseNotify()
	go func() {
		<-notify
		clientsMutex.Lock()
		delete(clients, messageChan)
		clientsMutex.Unlock()
		close(messageChan)
	}()

	// Send messages to the client
	for {
		msg, open := <-messageChan
		if !open {
			break
		}
		fmt.Fprintf(w, "data: %s\n\n", msg)
		flusher.Flush()
	}
}

func notifyClients(message string) {
	clientsMutex.Lock()
	defer clientsMutex.Unlock()
	for client := range clients {
		client <- message
	}
}
