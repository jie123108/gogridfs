package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"labix.org/v2/mgo"
)

// file path and content
type gridfile struct {
	Path string
	Data bytes.Buffer
}

// make gridfs, logger and config globally accessible
type gogridfs struct {
	GFS    *mgo.GridFS
	Logger *log.Logger
	Conf   config
}

var ggfs gogridfs

// config options to unmarshaled from json
type config struct {
	Servers          []string
	Logfile          string
	Database         string
	GridFSCollection string
	Field            string // _id, filename
	Listen           string
	HandlePath       string
	Debug            bool
	Mode             string
}

// load config from json file
func loadConfig(file string) (err error) {

	b_file, err := ioutil.ReadFile(file)
	if err != nil {
		return
	}

	err = json.Unmarshal(b_file, &ggfs.Conf)

	return
}

// fetch file from gridfs
func getFile(value string, field string) (file bytes.Buffer, filename string, err error) {

	var gfsFile *mgo.GridFile
	// open gridfile where value is the filename in GridFS
	if field == "_id" {
		gfsFile, err = ggfs.GFS.OpenId(value)
	} else {
		gfsFile, err = ggfs.GFS.Open(value)
	}

	if err != nil {
		return
	}

	filename = gfsFile.Name()

	// read file into buffer
	for {
		buffer := make([]byte, 4096)
		bytes_r, err := gfsFile.Read(buffer)

		if bytes_r > 0 {
			file.Write(buffer[:bytes_r])
		}

		if err != nil {
			break
		}
	}

	// non EOF error are to be handled
	if err != io.EOF {
		return
	}

	// close gridfile
	err = gfsFile.Close()
	if err != nil {
		return
	}

	return
}

// handle HTTP requests
func fileHandler(w http.ResponseWriter, r *http.Request) {

	// cut handlepath from URL path
	// remainder will be the filename to fetch from GridFS
	path := r.URL.Path[len(ggfs.Conf.HandlePath):]

	// print requested path when debugging
	if ggfs.Conf.Debug == true {
		ggfs.Logger.Println(path)
	}

	data, filename, err := getFile(path, ggfs.Conf.Field)

	// build the file struct
	file := gridfile{Path: path, Data: data}
	if err != nil {
		ggfs.Logger.Println(err)
	}
	// Content-Disposition: attachment; filename="$filename"
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	// print buffer to response writer
	fmt.Fprintf(w, "%s", file.Data.String())
}

func main() {

	// get config file from command line args
	var config_file = flag.String("config", "config.json", "Config file in JSON format")
	flag.Parse()

	// load config from JSON file
	err := loadConfig(*config_file)

	// panic on errors before the log file is in place
	if err != nil {
		panic(err)
	}

	// initialize log writer
	var writer io.Writer
	if ggfs.Conf.Logfile == "" {
		writer = os.Stdout
	} else {
		writer, err = os.OpenFile(ggfs.Conf.Logfile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0750)
		// panic on errors before the log file is in place
		if err != nil {
			panic(err)
		}
	}

	ggfs.Logger = log.New(writer, "", 5)

	// concatenate mongodb servers to single string of comma seperated servers
	var servers string
	for _, server := range ggfs.Conf.Servers {
		servers += (server + ",")
	}

	// determine mode
	// Strong (safe) => 2
	// Monotonic (fast) => 1
	// Eventual (faster) => 0
	// default => 2
	mode := mgo.Strong
	if strings.ToLower(ggfs.Conf.Mode) == "monotonic" {
		ggfs.Logger.Println("mgo connection mode: monotonic")
		mode = mgo.Monotonic
	} else if strings.ToLower(ggfs.Conf.Mode) == "eventual" {
		ggfs.Logger.Println("mgo connection mode: eventual")
		mode = mgo.Eventual
	}

	// die if no servers are configured
	if servers == "" {
		ggfs.Logger.Fatalln("No mongodb servers. Please adjust your config file.")
	}

	// connect to mongodb
	mgo_session, err := mgo.Dial(servers)
	mgo_session.SetMode(mode, true)
	if err != nil {
		ggfs.Logger.Fatalln(err)
	}
	defer mgo_session.Close()

	// get gridfs
	ggfs.GFS = mgo_session.DB(ggfs.Conf.Database).GridFS(ggfs.Conf.GridFSCollection)

	// run webserver
	http.HandleFunc(ggfs.Conf.HandlePath, fileHandler)
	http.ListenAndServe(ggfs.Conf.Listen, nil)
}
