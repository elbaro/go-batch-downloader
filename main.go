package main

import (
	"github.com/dustin/go-humanize"
	ui "github.com/gizak/termui"
	_ "github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	"go.uber.org/atomic"

	"io"
	"net/http"
	"os"

	"bufio"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DownloadProgress struct {
	name       string
	filesize   uint64
	downloaded atomic.Uint64
}

type Widgets struct {
	download_rate  *ui.Par
	total_download *ui.Gauge
	recent_list    *ui.List
}

var widgets *Widgets

type UiState struct {
	numRecent           int
	next_recent         atomic.Int32
	recent_downloads    []atomic.String
	download_completed  atomic.Uint32
	download_progresses []atomic.Value
	download_labels     []*ui.Par
	download_bars       []*ui.Gauge
	download_rate       atomic.Uint64
	download_rate_avg   float64
}

var state *UiState

func NewState(maxConcurentDownload int, numRecent int) *UiState {
	recent_downloads := make([]atomic.String, numRecent, numRecent)
	for i := 0; i < numRecent; i++ {
		recent_downloads[i].Store("")
	}
	download_progresses := make([]atomic.Value, maxConcurentDownload, maxConcurentDownload)
	download_labels := make([]*ui.Par, maxConcurentDownload, maxConcurentDownload)
	download_bars := make([]*ui.Gauge, maxConcurentDownload, maxConcurentDownload)
	for i := 0; i < maxConcurentDownload; i++ {
		download_progresses[i].Store(&DownloadProgress{})
		download_labels[i] = func() (p *ui.Par) {
			p = ui.NewPar("")
			p.Height = 1
			p.Border = false
			return
		}()
		download_bars[i] = func() (g *ui.Gauge) {
			g = ui.NewGauge()
			g.Height = 1
			g.Border = false
			return
		}()
	}
	return &UiState{
		numRecent:           numRecent,
		next_recent:         *atomic.NewInt32(0),
		recent_downloads:    recent_downloads,
		download_completed:  *atomic.NewUint32(0),
		download_progresses: download_progresses,
		download_labels:     download_labels,
		download_bars:       download_bars,
		download_rate:       *atomic.NewUint64(0),
	}
}

///////////

type Face struct {
	filename   string
	width      int
	height     int
	confidence float32
}

type ProgressReader struct {
	io.Reader
	Update func(r int)
}

func (pr *ProgressReader) Read(p []byte) (n int, err error) {
	n, err = pr.Reader.Read(p)
	pr.Update(n)
	return
}

var DEST_DIR string

func download(url string, worker_id int) error {
	tokens := strings.Split(url, "/")
	filename := tokens[len(tokens)-1]
	path := DEST_DIR + filename

	if _, err := os.Stat(path); err == nil {
		return nil
	}

	tmp_path := path + "_"

	f, err := os.Create(tmp_path)
	if err != nil {
		return errors.New("os.Create failed" + tmp_path)
	}

	filesize, err := func() (uint64, error) {
		res, err := http.Head(url)
		if err != nil {
			return 0, errors.New("http.Head Failed:" + url)
		}
		defer res.Body.Close()
		size, err := strconv.ParseUint(res.Header.Get("Content-Length"), 10, 64)
		if err != nil {
			return 0, errors.New("Content-Length not parsable:\n" + url + "\n" + res.Header.Get("Content-Length"))
		}
		return size, nil
	}()
	if err != nil {
		return err
	}

	label := fmt.Sprintf("%s (%6s)", url, humanize.Bytes(filesize))
	progress := &DownloadProgress{label, filesize, *atomic.NewUint64(0)}
	state.download_progresses[worker_id].Store(progress)

	res, err := http.Get(url)
	if err != nil {
		return errors.New("http.Get failed:" + url)
	}
	defer res.Body.Close()

	pr := &ProgressReader{res.Body, func(r int) {
		progress.downloaded.Add(uint64(r))
		state.download_rate.Add(uint64(r))
	}}

	_, err = io.Copy(f, pr)
	if err != nil {
		return errors.New("io.Copy failed:" + tmp_path + path)
	}

	// ~.jpg_ -> ~.jpg
	// if any, overwrite
	if err := os.Rename(tmp_path, path); err != nil {
		return errors.New("os.Rename failed:" + tmp_path + path)
	}

	abs_path, err := filepath.Abs(path)
	if err != nil {
		return errors.New("filepath.Abs failed:" + path)
	}
	i := int(state.next_recent.Add(1))
	state.recent_downloads[i%state.numRecent].Store(abs_path)
	return nil
}

func main() {
	if len(os.Args) != 3 {
		log.Panicln("usage: download [url-txt] [dest-dir]")
	}

	URL_PATH := os.Args[1]
	DEST_DIR = os.Args[2] + "/"

	maxConcurentDownload := 10
	numRecent := 10

	var wg sync.WaitGroup
	// load urls from urls.txt
	// url_ch := make(chan string)
	var urls []string

	// doesn't take long
	func() {
		// defer close(url_ch)

		f, err := os.Open(URL_PATH)
		if err != nil {
			log.Panicln(URL_PATH, " cannot be found", err)
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)

		for scanner.Scan() {
			url := scanner.Text()
			// url_ch <- url
			urls = append(urls, url)
		}
	}()

	n := len(urls)
	err := ui.Init()
	if err != nil {
		log.Panicln("ui.Init failed", err)
	}
	defer ui.Close()

	{
		state = NewState(maxConcurentDownload, numRecent)

		{
			widgets = &Widgets{}
			widgets.download_rate = ui.NewPar("")
			widgets.download_rate.BorderLabel = "Download Speed"
			widgets.download_rate.Height = 5
			widgets.total_download = ui.NewGauge()
			widgets.total_download.BorderLabel = "Total Download"
			labels := make([]ui.GridBufferer, len(state.download_labels), len(state.download_labels))
			for i, label := range state.download_labels {
				labels[i] = label
			}
			bars := make([]ui.GridBufferer, len(state.download_bars), len(state.download_bars))
			for i, bar := range state.download_bars {
				bars[i] = bar
			}
			widgets.recent_list = ui.NewList()
			widgets.recent_list.Height = numRecent + 2
			widgets.recent_list.BorderLabel = "Recent Downloads"
			widgets.recent_list.Items = make([]string, numRecent, numRecent)
			ui.Body.AddRows(
				ui.NewRow(
					ui.NewCol(6, 0, widgets.recent_list),
					ui.NewCol(6, 0, widgets.total_download, widgets.download_rate),
				),
				ui.NewRow(
					ui.NewCol(8, 0, labels...),
					ui.NewCol(4, 0, bars...),
				),
			)
		}

		p := ui.NewPar("Press q to quit")
		ui.Render(p)

		ui.Handle("/sys/kbd/q", func(ui.Event) {
			ui.StopLoop()
		})
		ui.Handle("/sys/kbd/C-x", func(ui.Event) {
			ui.StopLoop()
		})
		ui.Handle("/timer/1s", func(e ui.Event) {
			for i, bar := range state.download_bars {
				progress := state.download_progresses[i].Load().(*DownloadProgress)
				label := state.download_labels[i]
				label.Text = progress.name
				bar.Percent = int(float32(progress.downloaded.Load()) / (float32(progress.filesize) + 0.01) * 100)
			}

			download_rate := state.download_rate.Swap(0)
			widgets.download_rate.Text = fmt.Sprintf("%8s/s", humanize.Bytes(download_rate))
			dc := state.download_completed.Load()
			widgets.total_download.Label = fmt.Sprintf("%7d / %7d", dc, n)
			widgets.total_download.Percent = int(float32(dc) / (float32(n) + 0.01) * 100)

			for i := range widgets.recent_list.Items {
				widgets.recent_list.Items[i] = state.recent_downloads[i].Load()
			}
			ui.Body.Align()
			ui.Render(ui.Body)
		})
	}

	func() {
		downloadSemaphore := make(chan int, maxConcurentDownload)
		for i := 0; i < maxConcurentDownload; i++ {
			downloadSemaphore <- i
		}

		for _, url := range urls {
			// download
			wg.Add(1)
			worker := <-downloadSemaphore
			go func(url string) {
				defer wg.Done()

				for i := 0; i < 5; i++ {
					err := download(url, worker)
					if err == nil {
						break
					}
					time.Sleep(5 * time.Second)
				}

				downloadSemaphore <- worker
				state.download_completed.Add(1)
			}(url)
		}
	}()

	// wait for all downloads
	go func() {
		wg.Wait()
		fmt.Printf("Download Completed: %d\n", n)
		ui.StopLoop()
	}()

	ui.Loop()
}
