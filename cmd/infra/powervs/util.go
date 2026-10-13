package powervs

import (
	"fmt"
	"net/url"
)

// getStartToken parses the given url string and gets the 'start' query param
func getStartToken(nextUrlS string) (string, error) {
	nextUrl, err := url.Parse(nextUrlS)
	if err != nil || nextUrl == nil {
		return "", fmt.Errorf("could not parse next url for getting next resources %w", err)
	}

	start := nextUrl.Query().Get("start")
	return start, nil
}

// pagingHelper while listing resources, can use this to get the start token for getting the next set of resources for processing
// start token will get fetched from nextUrl returned by f and passed to the func f.
// f should take start as param and return three values isDone bool, nextUrl string, e error.
// isDone  - represents no need to iterate for getting next set of resources.
// nextUrl - if nextUrl is present, will try to get the start token and pass it to f for next set of resource processing.
// e       - if e is not nil, will break and return the error.
func pagingHelper(f func(string) (bool, string, error)) error {
	start := ""
	var err error
	for {
		isDone, nextUrl, e := f(start)

		if e != nil {
			err = e
			break
		}

		if isDone {
			break
		}

		// for paging over next set of resources getting the start token
		if nextUrl != "" {
			start, err = getStartToken(nextUrl)
			if err != nil {
				break
			}
		} else {
			break
		}
	}

	return err
}
