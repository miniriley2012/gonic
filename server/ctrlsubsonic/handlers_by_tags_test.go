package ctrlsubsonic

import (
	"net/url"
	"testing"
)

func TestGetArtists(t *testing.T) {
	runQueryCases(t, testController.ServeGetArtists, []*queryCase{
		{url.Values{}, "no_args", false},
	})
}

func TestGetArtist(t *testing.T) {
	runQueryCases(t, testController.ServeGetArtist, []*queryCase{
		{url.Values{"id": []string{"1"}}, "id_one", false},
		{url.Values{"id": []string{"2"}}, "id_two", false},
		{url.Values{"id": []string{"3"}}, "id_three", false},
	})
}

func TestGetAlbum(t *testing.T) {
	runQueryCases(t, testController.ServeGetAlbum, []*queryCase{
		{url.Values{"id": []string{"2"}}, "without_cover", false},
		{url.Values{"id": []string{"3"}}, "with_cover", false},
	})
}

func TestGetAlbumListTwo(t *testing.T) {
	runQueryCases(t, testController.ServeGetAlbumListTwo, []*queryCase{
		{url.Values{"type": []string{"alphabeticalByArtist"}}, "alpha_artist", false},
		{url.Values{"type": []string{"alphabeticalByName"}}, "alpha_name", false},
		{url.Values{"type": []string{"newest"}}, "newest", false},
		{url.Values{"type": []string{"random"}, "size": []string{"15"}}, "random", true},
	})
}

func TestSearchThree(t *testing.T) {
	runQueryCases(t, testController.ServeSearchThree, []*queryCase{
		{url.Values{"query": []string{"13"}}, "q_13", false},
		{url.Values{"query": []string{"ani"}}, "q_ani", false},
		{url.Values{"query": []string{"cert"}}, "q_cert", false},
	})
}
