# log-to-cube

High performance translation utility for parsing data into [Cube](http://square.github.com/cube/).

This project is a proof of concept - probably usable for production if you're
capable of fixing a bug or two and opening a pull request for it.

## Install from source

Until I release packages you'll need [go](http://golang.org/doc/install) installed

    $ git clone git@bitbucket.org:jkassemi/log-to-cube.git
    $ cd log-to-cube
    $ go build log-to-cube.go

You can then use the resulting binary. You can also [target a platform other than the one
you're compiling
on](http://dave.cheney.net/2013/07/09/an-introduction-to-cross-compilation-with-go-1-1).


## Operation

Log files are tailed, messages parsed and queued, and sent to Cube
asynchronously in batches. Failures are quueed to 25K messages, at which point
messages start dropping from the back of the queue until Cube becomes available
again. 

Adjustment of the batch size and concurrency may 

## Basics

The config.json file contains all configuration options. Configure the base url
for your Cube collector, and then an array of sources to tail. The following
tails all test.log files nested within the test directory, and posts to Cube
with a 'message' data attribue:

```json
{
  "cube": "http://localhost:1080",
  "sources": [{
    "glob": "test/**/test.log",
    "pattern": "(?P<message>.*)",
    "type": "test"
  }]
}
```

You can also specify paths directly instead of using a glob:

```json
{
  "cube": "http://localhost:1080",
  "sources": [{
    "path": "/var/log/nginx/access.log",
    "pattern": "(?P<host>\\S+) - - \\[(?P<time>[^\\]]+)\\] \"(?P<method>[A-Z]+) (?P<uri>\\S+).*\" (?P<status>\\S+) (?P<bytes>\\S+) \"(?P<referer>\\S+)\" \"(?P<agent>.*)\" \"(?P<forward>.*)\"",
    "type": "request",
  }]
}
```

Patterns must contain a named field - if they don't then no data will 
be collected.

## Field Typecasting

Fields are, by default, parsed into basic string fields. Several 'casts' are
avaiable to convert or parse fields into more advanced structures. The following
configuration converts the host and referer values into more detailed maps,
and converts bytes into an integer value.

```json
{
  "cube": "http://localhost:1080",
  "sources": [{
    "path": "/var/log/nginx/access.log",
    "pattern": "(?P<host>\\S+) - - \\[(?P<time>[^\\]]+)\\] \"(?P<method>[A-Z]+) (?P<uri>\\S+).*\" (?P<status>\\S+) (?P<bytes>\\S+) \"(?P<referer>\\S+)\" \"(?P<agent>.*)\" \"(?P<forward>.*)\"",
    "type": "request",
    "cast": {
      "host": "host",
      "bytes": "integer",
      "referer": "url"
    }
  }]
}
```

### string

Default cast, converts field into a string value.

### integer

Convert field into an integer value ("123" => 123)

### float

Convert field into a float value ("1.23" => 1.23)  

### url

Split a url into subcomponents

http://google.com/test?a=1#b

```json
{
  "Scheme": "http",
  "Opaque": "//google.com/test",
  "Host": "google.com",
  "Path": "/test",
  "RawQuery": "a=1",
  "Fragment": "b"
}
```

### host

Split a host into basic subcomponents

subdomain.example.com

```json
{
  "subdomain": "subdomain",
  "domain": "example.com",
  "full": "subdomain.example.com"
} 
```

## Similar Projects

* [Dendrite](https://github.com/omc/dendrite)
