package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gorilla/mux"
	"github.com/jaytaylor/html2text"
)

const poemsPath = `./poems.txt`

func cleanup(sel *goquery.Selection) *goquery.Selection {
	sel.Find("dt").WrapHtml("<div></div>")
	sel.Find("dd").WrapHtml("<div></div>")
	return sel
}

func main() {
	poemBytes, err := ioutil.ReadFile(poemsPath)
	if err != nil {
		log.Fatal(err)
	}
	poemsStart := strings.Split(strings.TrimSpace(string(poemBytes)), "\n")
	poems := make([]string, len(poemsStart))
	for i, p := range poemsStart {
		poems[i] = strings.TrimSpace(p)
	}
	wrong, err := loadWrong()
	if err != nil {
		log.Printf("Failed to load wrong file, initializing full...: %s", err)
		wrong = map[int]struct{}{}
		for i := range poems {
			wrong[i] = struct{}{}
		}
	}

	rand.Seed(time.Now().UTC().UnixNano())

	r := mux.NewRouter()

	r.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<p>Correct: %d/%d</p>", len(poems)-len(wrong), len(poems))
		for i := range poems {
			correct := "Correct"
			if _, ok := wrong[i]; ok {
				correct = "Wrong"
			}
			fmt.Fprintf(w, `<a href="poem?q=%d">%d</a>: %s<br>`, i, i, correct)
		}
		footer(w)
	})

	r.HandleFunc("/randpoem", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "./poem?q="+strconv.Itoa(rand.Intn(len(poems))), 302)
	})

	r.HandleFunc("/wrong", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		i, err := strconv.Atoi(r.FormValue("q"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		wrong[i] = struct{}{}
		if err := saveWrong(wrong); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		for i := range wrong {
			http.Redirect(w, r, "./poem?q="+strconv.Itoa(i), 302)
			return
		}
		w.Write([]byte("You've answered all of them correctly!"))
		footer(w)
	})

	r.HandleFunc("/correct", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		i, err := strconv.Atoi(r.FormValue("q"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if _, ok := wrong[i]; ok {
			delete(wrong, i)
			if err := saveWrong(wrong); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		for i := range wrong {
			http.Redirect(w, r, "./poem?q="+strconv.Itoa(i), 302)
			return
		}
		w.Write([]byte("<p>You've answered all of them correctly!</p>"))
		footer(w)
	})

	r.HandleFunc("/poem", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		i, err := strconv.Atoi(r.FormValue("q"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		poemURL := poems[i]
		parsedURL, err := url.Parse(poemURL)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		w.Write([]byte(`<head><meta name="viewport" content="width=device-width, initial-scale=1" />
<style>
		body {
			font-family: 'Roboto';
		}
		pre {
			white-space: pre-wrap;
			font: inherit;
		}
		.answer {
			color: white;
		}
		.answer:hover {
			color: blue;
		}
		</style></head>`))

		log.Printf("rendering: %q", poemURL)
		fmt.Fprintf(w, "<a href=\"%s\">Poem</a>", poemURL)
		resp, err := http.Get(poemURL)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer resp.Body.Close()
		htmlbody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		doc, err := goquery.NewDocumentFromReader(bytes.NewBuffer(htmlbody))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var title, author, body, bodyText string
		title = doc.Find("title").First().Text()
		switch parsedURL.Host {
		case "www.poetryfoundation.org":
			sel := doc.Find(".poem")
			sel.Find(`[style="display: none;"]`).Remove()
			body, err = sel.Html()
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		case "www.poemhunter.com":
			body, err = doc.Find(".KonaBody p").Html()
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		case "www.poets.org":
			author = doc.Find("span.node-title").First().Text()
			sel := doc.Find("pre").First()
			bodyText = sel.Text()
			body, err = goquery.OuterHtml(sel)
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		case "www.bartleby.com":
			start := bytes.Index(htmlbody, []byte(`<!-- BEGIN CHAPTER -->`))
			end := bytes.Index(htmlbody, []byte(`<!-- END CHAPTER -->`))
			if start >= 0 && end >= 0 {
				body = fmt.Sprintf("<table>%s</table>", htmlbody[start:end])
			} else {
				body, err = goquery.OuterHtml(doc.Find("form table > tbody > tr > td > table").Eq(4))
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
			}
		case "www.daypoems.net":
			sel := doc.Find(".poem")
			sel.Find("h1, h3, b, a").Remove()
			body, err = goquery.OuterHtml(sel)
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		case "english.emory.edu":
			sel := doc.Find("td:first-child > p:not(:nth-child(2))").First()
			sel.Find("b, a").Remove()
			body, err = goquery.OuterHtml(sel)
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		case "www.poetry-archive.com":
			sel := cleanup(doc.Find("dl").First())
			body, err = goquery.OuterHtml(sel)
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		case "boppin.com":
			sel := cleanup(doc.Find("dl").First())
			sel.Find("p").Remove()
			body, err = goquery.OuterHtml(sel)
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		case "www.rc.umd.edu":
			sel := doc.Find(".node-content p").First()
			body, err = goquery.OuterHtml(sel)
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		case "mural.uv.es":
			sel := doc.Find("p[align=center]").First()
			body, err = goquery.OuterHtml(sel)
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
			title = doc.Find("b").Eq(2).Text()
		default:
			http.Error(w, fmt.Sprintf("unknown host %q", parsedURL.Host), 500)
		}

		fmt.Fprintf(w, `
		<button onclick='document.querySelector("div").style.display="block";'>View Whole</button>
		<div style="display:none">%s<hr></div>`, body)
		if len(bodyText) == 0 {
			bodyText, err = html2text.FromString(body)
			if err != nil {
				handleError(w, err)
				return
			}
		}
		fmt.Fprintf(w, `<pre>%s</pre>`, randLines(bodyText))
		fmt.Fprintf(w, `<div class="answer">
%s
</br>
 %s
</div>`, title, author)
		fmt.Fprintf(w, `<div><a href="correct?q=%d">Correct</a> <a href="wrong?q=%d">Wrong</a></div>`, i, i)
		footer(w)
	})

	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		footer(w)
	})

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s", r.URL)
	})

	log.Println("Listening :8080...")
	log.Fatal(http.ListenAndServe(":8080", r))

}

func randLines(text string) string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if len(strings.TrimSpace(l)) == 0 {
			continue
		}
		lines = append(lines, l)
	}

	start := 0
	lcount := len(lines) - 5
	if lcount > 0 {
		start = rand.Intn(lcount)
	}
	if start < 0 {
		start = 0
	}
	end := start + 5
	if end > len(lines) {
		end = len(lines)
	}

	return strings.Join(lines[start:end], "\n")
}

const wrongFile = `wrong.json`

func loadWrong() (map[int]struct{}, error) {
	bytes, err := ioutil.ReadFile(wrongFile)
	if err != nil {
		return nil, err
	}
	var ints []int
	if err := json.Unmarshal(bytes, &ints); err != nil {
		return nil, err
	}
	wrong := map[int]struct{}{}
	for _, i := range ints {
		wrong[i] = struct{}{}
	}
	return wrong, nil
}

func saveWrong(wrong map[int]struct{}) error {
	ints := make([]int, 0, len(wrong))
	for i := range wrong {
		ints = append(ints, i)
	}
	bytes, err := json.Marshal(ints)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(wrongFile, bytes, 0755)
}

func footer(w io.Writer) {
	fmt.Fprintf(w, `<p><a href="randpoem">Random</a> <a href="list">List All</a></p>`)
}

func handleError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), 500)
	footer(w)
}
