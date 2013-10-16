package main

import (
  "testing"
  "regexp"
  "reflect"
  "net/http"
  "net/http/httptest"
  "io/ioutil"
  "encoding/json"
  "github.com/jkassemi/tail"
)

func init(){
  config = &Config{}
}

var castTests = []struct {
  castType      string
  castValue     string
  castOut       interface{}
}{
  {"string", "Hello world", "Hello world"},
  {"integer", "A", 0},
  {"integer", "1", 1},
  {"host", "google.com", map[string]string{"subdomain": "", "domain": "google.com", "full": "google.com"}},
  {"host", "www.google.com", map[string]string{"subdomain": "www", "domain": "google.com", "full": "www.google.com"}},
  {"host", "abc.def.google.com", map[string]string{"subdomain": "abc.def", "domain": "google.com", "full": "abc.def.google.com"}},
}

func TestCastMethods(t *testing.T){
  for i, tc := range castTests {
    ret := casts[tc.castType](tc.castValue)

    if !reflect.DeepEqual(ret, tc.castOut) {
      t.Errorf("%d: cast %s ( %s ) return value expected: %q returned: %q", i, tc.castType, tc.castValue, tc.castOut, ret)
    }
  }
}

var batchPartitionTests = []struct {
  recordCount         int
  batchSize           int
  batchCount          int
}{
  {500, 5, 100},
  {6, 5, 2},
  {4, 5, 1},
  {0, 5, 0},
}

func TestBatchesPartitionedToCube(t *testing.T){
  for i, tc := range batchPartitionTests {
    s := getCubeMockServer()

    for j:=0; j<tc.recordCount; j++ {
      cubeEntries = append(cubeEntries, &CubeEntry{})
    }

    partitionBatchesToCube(tc.batchSize)

    s.Close()

    if s.batchesProcessed != tc.batchCount {
      t.Errorf("%d: batched %d times, expected %d", i, s.batchesProcessed, tc.batchCount)
    }

    if s.recordsProcessed != tc.recordCount {
      t.Errorf("%d: %d records sent, expected %d", i, s.recordsProcessed, tc.recordCount)
    }
  }
}

func TestFailuresEventuallyMoveToCube(t *testing.T){
  for i, tc := range batchPartitionTests {
    if tc.recordCount == 0 {
      continue
    }

    s := getCubeMockServer()
    s.handlerFunc = cubeMockServerFail

    for j:=0; j < tc.recordCount; j++ {
      cubeEntries = append(cubeEntries, &CubeEntry{})
    }

    partitionBatchesToCube(tc.batchSize)

    if len(cubeEntries) == 0 {
      t.Fatalf("%d: expected failures, received none", i)
    }

    s.handlerFunc = cubeMockServerPass

    partitionBatchesToCube(tc.batchSize)

    if s.recordsProcessed != tc.recordCount {
      t.Errorf("%d: %d records sent, expected %d", i, s.recordsProcessed, tc.recordCount)
    }

    s.Close()
  }
}

var consumeTests = []struct {
  pattern             string
  line                string
  expectations        map[string]interface{}
  casts               map[string]string
}{
  {"(?P<message>.*)", "Hello World", map[string]interface{}{"message":"Hello World"}, map[string]string{}},
  {"(?P<one>.*?),(?P<two>.*)", "Hello,2", map[string]interface{}{"one":"Hello","two":2}, map[string]string{"one":"string","two":"integer"}},
}

func TestPatternConsumption(t *testing.T){
  source := &Source{}

  for i, tc := range consumeTests {
    cubeEntries = make([]*CubeEntry, 0)

    source.regexp = regexp.MustCompile(tc.pattern)
    source.regexpNames = source.regexp.SubexpNames()
    source.Cast = tc.casts

    line := &tail.Line{Text: tc.line}

    source.consume(line)

    lastEntry := cubeEntries[0]

    for k, v := range tc.expectations {
      if lastEntry.Data[k] != v {
        t.Errorf("%d: Expected %s to contain %q, contained %q", i, k, v, lastEntry.Data[k])
      }
    }
  }
}

type cubeMockServer struct {
  ts                *httptest.Server
  handlerFunc       func(ms *cubeMockServer, w http.ResponseWriter, r*http.Request)
  batchesProcessed  int
  recordsProcessed  int
  batchesFailed     int
}

func cubeMockServerPass(mockServer *cubeMockServer, w http.ResponseWriter, r *http.Request){
  defer r.Body.Close()

  mockServer.batchesProcessed += 1

  var records []interface{}

  b, _ := ioutil.ReadAll(r.Body)

  json.Unmarshal(b, &records)

  mockServer.recordsProcessed += len(records)
}

func cubeMockServerFail(mockServer *cubeMockServer, w http.ResponseWriter, r *http.Request){
  mockServer.batchesFailed += 1

  w.WriteHeader(500)
}

var last bool

func cubeMockServerHalf(mockServer *cubeMockServer, w http.ResponseWriter, r *http.Request){
  last = !last

  if last {
    cubeMockServerPass(mockServer, w, r)

  } else {
    cubeMockServerFail(mockServer, w, r)

  }
}

func getCubeMockServer() (mockServer *cubeMockServer) {
  mockServer = &cubeMockServer{
    batchesProcessed: 0,
    recordsProcessed: 0,
    batchesFailed: 0,
    handlerFunc: cubeMockServerPass,
  }

  mockServer.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
    mockServer.handlerFunc(mockServer, w, r)
  }))

  config.Cube = mockServer.ts.URL

  return mockServer
}

func (mockServer *cubeMockServer) Close() {
  mockServer.ts.Close()
}

