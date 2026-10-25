// Package htmlparser extracts the download state and price of a Bandcamp
// album page. The CSS selectors mirror the TypeScript implementation.
package htmlparser

import (
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/br4instormer/bandcamp-price-crawler/internal/cost"
)

// onlyDigitsAndDot recognises prices written as bare numbers, e.g. "7.50".
var onlyDigitsAndDot = regexp.MustCompile(`^[0-9.]+$`)

// ParseCost reads an album page and returns its cost state and price.
func ParseCost(html string) (cost.Cost, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return cost.Cost{}, err
	}
	return parseCost(doc), nil
}

func parseCost(doc *goquery.Document) cost.Cost {
	switch {
	case doc.Find(".buyItem.digital").Length() == 0:
		return cost.New(cost.NotAbleToDownload, "")
	case freeDownload(doc):
		return cost.New(cost.FreeDownload, "")
	case nameYourPrice(doc):
		return cost.New(cost.NameYourPrice, "")
	default:
		return cost.New(cost.RegularDownload, priceWithCurrency(doc))
	}
}

func freeDownload(doc *goquery.Document) bool {
	return strings.TrimSpace(doc.Find(".buyItem.digital .compound-button .download-link").Text()) == "Free Download"
}

func nameYourPrice(doc *goquery.Document) bool {
	return strings.TrimSpace(doc.Find(".buyItem.digital .buyItemNyp").Text()) == "name your price"
}

// priceWithCurrency returns the buy button price, appending the currency code
// when the price itself carries no currency sign.
func priceWithCurrency(doc *goquery.Document) string {
	price := strings.TrimSpace(doc.Find(".buyItem.digital .nobreak").Find(".base-text-color").Text())
	if onlyDigitsAndDot.MatchString(price) {
		code := strings.TrimSpace(doc.Find(".buyItem.digital .nobreak .secondaryText").First().Text())
		price = strings.TrimSpace(price + " " + code)
	}
	return price
}
