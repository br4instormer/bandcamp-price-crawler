package htmlparser

import (
	"testing"

	"github.com/br4instormer/bandcamp-price-crawler/internal/cost"
)

func TestParseCost(t *testing.T) {
	tests := []struct {
		name  string
		html  string
		state cost.State
		price string
	}{
		{
			name:  "page without digital download",
			html:  `<html><body><div class="buyItem"></div></body></html>`,
			state: cost.NotAbleToDownload,
			price: "Not available to download",
		},
		{
			name:  "empty page",
			html:  ``,
			state: cost.NotAbleToDownload,
			price: "Not available to download",
		},
		{
			name: "free download",
			html: `<div class="buyItem digital">
					<div class="compound-button"><a class="download-link"> Free Download </a></div>
				</div>`,
			state: cost.FreeDownload,
			price: "Free download",
		},
		{
			name: "name your price",
			html: `<div class="buyItem digital">
					<div class="compound-button"><a class="download-link">Buy</a></div>
					<h3 class="buyItemNyp"> name your price </h3>
				</div>`,
			state: cost.NameYourPrice,
			price: "Name your price",
		},
		{
			name: "regular price with currency sign",
			html: `<div class="buyItem digital">
					<div class="buyItemNyp">buy now</div>
					<div class="nobreak"><span class="base-text-color"> $7.50 </span></div>
				</div>`,
			state: cost.RegularDownload,
			price: "$7.50",
		},
		{
			name: "bare price gains currency code",
			html: `<div class="buyItem digital">
					<div class="nobreak">
						<span class="base-text-color">7.50</span>
						<span class="secondaryText"> USD </span>
						<span class="secondaryText">EUR</span>
					</div>
				</div>`,
			state: cost.RegularDownload,
			price: "7.50 USD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCost(tt.html)
			if err != nil {
				t.Fatalf("ParseCost() error = %v", err)
			}
			if got.State != tt.state {
				t.Errorf("state = %d, want %d", got.State, tt.state)
			}
			if got.Price != tt.price {
				t.Errorf("price = %q, want %q", got.Price, tt.price)
			}
		})
	}
}

func TestReady(t *testing.T) {
	tests := []struct {
		name string
		html string
		want bool
	}{
		{
			name: "real album page carries the blob",
			html: `<div class="buyItem digital"></div>
				<script id="collectors-data" data-blob="{&quot;thumbs&quot;:[]}"></script>`,
			want: true,
		},
		{
			name: "client challenge placeholder",
			html: `<html><head><title>Client Challenge</title></head>
				<body><script src="/_fs-ch-1T1wmsGaOgGaSxcX/script.js?reload=true"></script></body></html>`,
			want: false,
		},
		{
			name: "collectors-data without the blob attribute",
			html: `<script id="collectors-data"></script>`,
			want: false,
		},
		{"empty page", ``, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Ready(tt.html); got != tt.want {
				t.Errorf("Ready() = %v, want %v", got, tt.want)
			}
		})
	}
}
