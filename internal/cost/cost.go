// Package cost describes the download state of a Bandcamp album and its price.
package cost

// State classifies what a parsed album page offers for a digital download.
type State int

const (
	NotAbleToDownload State = iota
	FreeDownload
	NameYourPrice
	RegularDownload
)

// States lists every state in declaration order.
const States = 4

var stateMessages = [States]string{
	NotAbleToDownload: "Not available to download",
	FreeDownload:      "Free download",
	NameYourPrice:     "Name your price",
	RegularDownload:   "",
}

// Cost is the parse result of a single album page.
type Cost struct {
	State State
	Price string
}

// New builds a Cost; an empty price falls back to the message of the state,
// mirroring the TypeScript constructor (price ?? Message[state]).
func New(state State, price string) Cost {
	if price == "" {
		price = stateMessages[state]
	}
	return Cost{State: state, Price: price}
}

// IsFree reports whether the album is a free download.
func (c Cost) IsFree() bool {
	return c.State == FreeDownload
}
