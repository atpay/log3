package main

import (
	"bytes"
	"code.google.com/p/gocask"
	"encoding/json"
	"github.com/jkassemi/tail"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Cube    string    `json:'cube'`
	Sources []*Source `json:'sources'`
}

type Source struct {
	Glob        string            `json:'glob'`
	Path        string            `json:'path'`
	Pattern     string            `json:'pattern'`
	Type        string            `json:'type'`
	Cast        map[string]string `json:'cast'`
	regexp      *regexp.Regexp
	regexpNames []string
}

type CubeEntry struct {
	Type string                 `json:"type"`
	Time string                 `json:"time"`
	Data map[string]interface{} `json:"data"`
}

const (
	dbfile      = "data.db"
	configfile  = "config.json"
	concurrency = 5
	batchsize   = 500
)

var cubeLock sync.Mutex
var cubeEntries []*CubeEntry
var config *Config
var casts map[string](func(string) interface{})

var watching []string

func init() {
	casts = map[string](func(string) interface{}){
		"string":  castString,
		"integer": castInteger,
		"host":    castHost,
		"url":     castUrl,
		"float":   castFloat,
	}
}

func castString(v string) interface{} {
	return v
}

func castInteger(v string) interface{} {
	i, _ := strconv.Atoi(v)
	return i
}

func castHost(v string) interface{} {
	parts := strings.Split(v, ".")

	domain := strings.Join(parts[len(parts)-2:], ".")
	subdomain := strings.Join(parts[0:len(parts)-2], ".")

	return map[string]string{
		"subdomain": subdomain,
		"domain":    domain,
		"full":      v,
	}
}

func castUrl(v string) interface{} {
	u, _ := url.Parse(v)
	return u
}

func castFloat(v string) interface{} {
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

func getSeekInfo(path string) (*tail.SeekInfo, error) {
	cask, e := gocask.NewGocask(dbfile)
	defer cask.Close()

	if e != nil {
		return nil, e
	}

	b, e := cask.Get(path)

	var seekInfo tail.SeekInfo

	if e == gocask.ErrKeyNotFound {
		seekInfo.Offset = 0
		return &seekInfo, nil

	} else if e != nil {
		return nil, e

	}

	e = json.Unmarshal(b, &seekInfo)

	if e != nil {
		return nil, e
	}

	return &seekInfo, nil
}

func putSeekInfo(path string, seekInfo *tail.SeekInfo) error {
	cask, e := gocask.NewGocask(dbfile)
	defer cask.Close()

	if e != nil {
		return e
	}

	b, e := json.Marshal(seekInfo)

	if e != nil {
		return e
	}

	return cask.Put(path, b)
}

func loadConfig() *Config {
	config := &Config{}

	b, e := ioutil.ReadFile(configfile)

	if e != nil {
		panic(e.Error())
	}

	e = json.Unmarshal(b, &config)

	if e != nil {
		panic(e.Error())
	}

	return config
}

func (source *Source) watchPath(path string) {
	for i := 0; i < len(watching); i += 1 {
		if watching[i] == path {
			return
		}
	}

	watching = append(watching, path)

	log.Print("watching path ", path)

	seekInfo, e := getSeekInfo(path)

	if e != nil {
		panic(e.Error())
	}

	t, e := tail.TailFile(path, tail.Config{
		Location:  seekInfo,
		ReOpen:    true,
		MustExist: false,
		Follow:    true,
	})

	changed := false

	go func() {
		for {
			time.Sleep(500 * time.Millisecond)

			if changed {
				changed = false
				putSeekInfo(path, seekInfo)
			}
		}
	}()

	for {
		line := <-t.Lines

		if line == nil {
			time.Sleep(1 * time.Second)

			if seekInfo.Offset != 0 {
				seekInfo.Offset = 0
				changed = true
			}

			continue
		}

		seekInfo.Offset += int64(len(line.Text))

		changed = true

		source.consume(line)
	}
}

func (source *Source) watch() {
	if source.Glob != "" {
		matches, e := filepath.Glob(source.Glob)

		if e != nil {
			panic(e.Error())
		}

		for _, path := range matches {
			go source.watchPath(path)
		}
	} else if source.Path != "" {
		go source.watchPath(source.Path)

	}
}

func (source *Source) cast(key string, value string) interface{} {
	castName := source.Cast[key]

	if castName == "" || casts[castName] == nil {
		return value
	}

	return casts[castName](value)
}

func (source *Source) consume(line *tail.Line) {
	// parse out line and push into cube

	m := source.regexp.FindStringSubmatch(line.Text)

	if m == nil {
		return
	}

	d := make(map[string]interface{})

	for i, v := range source.regexpNames {
		if v != "" {
			d[v] = source.cast(v, m[i])
		}
	}

	v := &CubeEntry{
		Type: source.Type,
		Time: time.Now().Format(time.RFC3339),
		Data: d,
	}

	cubeEntries = append(cubeEntries, v)
}

func sendBatchToCube(batch []*CubeEntry) bool {
	//log.Print("cube: processing ", len(batch), " entries")

	b, e := json.Marshal(batch)

	if e != nil {
		panic(e.Error())
	}

	resp, e := http.Post(config.Cube+"/1.0/event/put", "application/json", bytes.NewReader(b))

	if e != nil {
		return false
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return false

	} else {
		return true

	}
}

func partitionBatchesToCube(size int) {
	if len(cubeEntries) == 0 {
		return
	}

	cubeLock.Lock()

	partCount := int(math.Ceil(float64(len(cubeEntries)) / float64(size)))

	parts := make([][]*CubeEntry, partCount)

	log.Print("log-to-cube: preparing ", len(cubeEntries), " records for upload in ", partCount, " batches")

	for i := 0; i < partCount; i += 1 {
		start := i * size
		stop := start + size

		if stop > len(cubeEntries) {
			parts[i] = cubeEntries[start:]

		} else {
			parts[i] = cubeEntries[start:stop]

		}
	}

	cubeEntries = cubeEntries[0:0]

	cubeLock.Unlock()

	done := make(chan int, partCount)

	concurrency := concurrency

	if concurrency > partCount {
		concurrency = partCount
	}

	concurrent := make(chan int, concurrency)

	for _, part := range parts {
		concurrent <- 1

		part := part

		go func() {
			if !sendBatchToCube(part) {
				cubeLock.Lock()
				cubeEntries = append(cubeEntries, part...)
				cubeLock.Unlock()
			}

			<-concurrent
			done <- 1
		}()
	}

	for i := 0; i < partCount; i += 1 {
		<-done
	}
}

func init() {
	cubeEntries = make([]*CubeEntry, 0)
}

func main() {
	config = loadConfig()
	sources := config.Sources

	for _, source := range sources {
		source.regexp = regexp.MustCompile(source.Pattern)
		source.regexpNames = source.regexp.SubexpNames()

		source.watch()
	}

	for {
		partitionBatchesToCube(batchsize)
		time.Sleep(5 * time.Second)
	}
}
