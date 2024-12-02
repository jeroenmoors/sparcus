package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Embed de templates map
//
//go:embed html/*
var htmlFS embed.FS

type Config struct {
	HandlersPath string
	DataFile     string
	MaxEvents    int
	GraphiteHost string
	GraphitePort int
}

type Value struct {
	Numbers   []float64 `json:"numbers"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

type Event struct {
	URI       string    `json:"uri"`
	Script    string    `json:"script"`
	Timestamp time.Time `json:"timestamp"`
	STDOUT    string    `json:"stdout"`
	STDERR    string    `json:"stderr"`
}

type Executable struct {
	Path        string `json:"path"`
	Script      string `json:"script"`
	Description string `json:"description"`
}

var config Config
var values map[string]Value
var events []Event

func init() {
	values = make(map[string]Value)
	config = Config{
		HandlersPath: "/var/lib/sparcus/handlers",
		DataFile:     "/var/lib/sparcus/data.json",
		MaxEvents:    250,
		GraphiteHost: "localhost",
		GraphitePort: 2003,
	}

	fmt.Println("Handlers path:", config.HandlersPath)
	fmt.Println("Data file:", config.DataFile)
}

func shutdown() {
	fmt.Println("Shutting down server...")
	fmt.Println("Writing data to disk...")
	data := map[string]interface{}{
		"values": values,
		"events": events,
	}

	file, err := os.Create(config.DataFile)
	if err != nil {
		fmt.Println("Error creating data file:", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		fmt.Println("Error encoding data to JSON:", err)
	}
}

func main() {
	defer shutdown()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		shutdown()
		os.Exit(1)
	}()

	fmt.Println("Loading data from disk...")
	file, err := os.Open(config.DataFile)
	if err != nil {
		fmt.Println("Error opening data file:", err)
	} else {
		defer file.Close()
		decoder := json.NewDecoder(file)
		data := map[string]interface{}{}
		if err := decoder.Decode(&data); err != nil {
			fmt.Println("Error decoding data file:", err)
		} else {
			if v, ok := data["values"].(map[string]interface{}); ok {
				for key, val := range v {
					valueBytes, err := json.Marshal(val)
					if err != nil {
						fmt.Println("Error marshalling value:", err)
						continue
					}
					var value Value
					if err := json.Unmarshal(valueBytes, &value); err != nil {
						fmt.Println("Error unmarshalling value:", err)
						continue
					}
					values[key] = value
				}
			}
			if e, ok := data["events"].([]interface{}); ok {
				for _, event := range e {
					eventBytes, err := json.Marshal(event)
					if err != nil {
						fmt.Println("Error marshalling event:", err)
						continue
					}
					var evt Event
					if err := json.Unmarshal(eventBytes, &evt); err != nil {
						fmt.Println("Error unmarshalling event:", err)
						continue
					}
					events = append(events, evt)
				}
			}
		}
	}

	fmt.Println("Starting server...")

	http.HandleFunc("/set/", setHandler)
	http.HandleFunc("/get/", getHandler)
	http.HandleFunc("/", adminHandler)

	fmt.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Println("Error starting server:", err)
	}
}

func adminHandler(w http.ResponseWriter, r *http.Request) {
	var tmpl *template.Template
	var err error
	fmt.Println("HTTP Request for", r.URL.Path)
	if r.URL.Path == "/" {
		tmpl = template.Must(template.ParseFS(htmlFS, "html/index.html"))
		err = tmpl.Execute(w, nil)
		if err != nil {
			http.Error(w, "Error rendering page: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if r.URL.Path == "/ajax/status" {
		w.Header().Set("Content-Type", "application/json")
		jsonData, err := json.Marshal(values)
		if err != nil {
			http.Error(w, "Error encoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(jsonData)
		return
	}

	if r.URL.Path == "/ajax/events" {
		w.Header().Set("Content-Type", "application/json")
		jsonData, err := json.Marshal(events)
		if err != nil {
			http.Error(w, "Error encoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(jsonData)
		return
	}

	if r.URL.Path == "/ajax/handlers" {
		w.Header().Set("Content-Type", "application/json")
		jsonData, err := json.Marshal(scanForExecutables())
		if err != nil {
			http.Error(w, "Error encoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(jsonData)
		return
	}

	content, err := htmlFS.ReadFile("html" + r.URL.Path)
	if err == nil {
		switch filepath.Ext(r.URL.Path) {
		case ".html":
			w.Header().Set("Content-Type", "text/html")
		case ".css":
			w.Header().Set("Content-Type", "text/css")
		case ".js":
			w.Header().Set("Content-Type", "application/javascript")
		case ".png":
			w.Header().Set("Content-Type", "image/png")
		case ".jpg", ".jpeg":
			w.Header().Set("Content-Type", "image/jpeg")
		case ".gif":
			w.Header().Set("Content-Type", "image/gif")
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
		}
	}
	if err != nil {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	w.Write(content)
}

func getHandler(w http.ResponseWriter, r *http.Request) {
	var value string
	var err error
	var query url.Values
	var paramAverage string
	var paramFormat string

	fmt.Println("Received request for:", r.URL.Path)
	uri := r.URL.Path
	uri = strings.TrimPrefix(uri, "/get/")
	uri = strings.ReplaceAll(uri, "/", ".")
	fmt.Println("Modified URI:", uri)

	query = r.URL.Query()
	paramAverage = query.Get("average")
	paramFormat = query.Get("format")

	if paramAverage == "" {
		value, err = getKeyLatest(uri)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
	} else {
		var count int
		count, err = strconv.Atoi(paramAverage)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		value, err = getKeyAverage(uri, count)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
	}

	timestamp := values[uri].Timestamp.Unix()
	if paramFormat == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"value": %s, "timestamp": %d}`, value, timestamp)))
		return
	}

	if paramFormat == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Write([]byte(fmt.Sprintf("%d,%s", timestamp, value)))
		return
	}

	if paramFormat == "pipe" {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(fmt.Sprintf("%d|%s", timestamp, value)))
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(fmt.Sprintf("%s", value)))
}

func setHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received request for:", r.URL.Path)
	uri := r.URL.Path
	uri = strings.TrimPrefix(uri, "/set/")
	uriDotted := strings.ReplaceAll(uri, "/", ".")
	fmt.Println("Dotted URI:", uriDotted)

	w.WriteHeader(http.StatusOK)

	query := r.URL.Query()
	paramValue := query.Get("value")
	if paramValue == "" {
		w.Write([]byte("Set: " + uriDotted + " no value provided"))
		fmt.Println("Query parameter 'value' was not set")
		setKey(uriDotted, "")
		if config.GraphiteHost != "" && config.GraphitePort != 0 {
			graphiteSend(uriDotted, "1")
		}
	} else {
		w.Write([]byte("Set: " + uriDotted + " to '" + paramValue + "'"))
		fmt.Println("Query parameter 'value' was set to:", paramValue)
		setKey(uriDotted, paramValue)
		if config.GraphiteHost != "" && config.GraphitePort != 0 {
			graphiteSend(uriDotted, paramValue)
		}
	}

	executables, err := scanForExecutablesInPath(config.HandlersPath, uri)
	if err != nil {
		fmt.Println("Error scanning for executables:", err)
		return
	} else {
		fmt.Println("Found executables:", executables)
	}

	for _, executable := range executables {
		fmt.Println("Executing:", executable)
		os.Setenv("EVENT_VALUE", paramValue)
		os.Setenv("EVENT_PATH", uri)
		os.Setenv("EVENT_PATH_DOTTED", uriDotted)
		var val string
		val, err = getKeyAverage(uriDotted, 3)
		os.Setenv("EVENT_VALUE_AVG_3", fmt.Sprintf("%f", val))
		val, err = getKeyAverage(uriDotted, 5)
		os.Setenv("EVENT_VALUE_AVG_5", fmt.Sprintf("%f", val))
		val, err = getKeyAverage(uriDotted, 10)
		os.Setenv("EVENT_VALUE_AVG_10", fmt.Sprintf("%f", val))

		cmd := exec.Command(executable)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			fmt.Println("Error creating StdoutPipe for Cmd:", err)
			continue
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			fmt.Println("Error creating StderrPipe for Cmd:", err)
			continue
		}

		if err := cmd.Start(); err != nil {
			fmt.Println("Error starting Cmd:", err)
			continue
		}

		stdoutBytes, _ := io.ReadAll(stdout)
		stderrBytes, _ := io.ReadAll(stderr)

		if err := cmd.Wait(); err != nil {
			fmt.Println("Error waiting for Cmd:", err)
		}

		event := Event{
			URI:       uriDotted,
			Script:    executable,
			Timestamp: time.Now(),
			STDOUT:    string(stdoutBytes),
			STDERR:    string(stderrBytes),
		}
		events = append(events, event)
		if len(events) > 250 {
			events = events[len(events)-250:]
		}
	}
}

func setKey(key string, value string) error {
	v := values[key]
	v.Timestamp = time.Now()
	val, err := strconv.ParseFloat(value, 64)
	if err == nil {
		v.Numbers = append(v.Numbers, val)
		if len(v.Numbers) > 10 {
			v.Numbers = v.Numbers[1:]
		}
	} else {
		fmt.Println("Non nummeric 'value':", value)
		v.Text = value
	}
	fmt.Println("Setting key:", key, "to:", value)
	values[key] = v
	return nil
}

func getKeyLatest(key string) (string, error) {
	val, ok := values[key]
	if ok && val.Text != "" {
		return val.Text, nil
	}

	if ok && len(val.Numbers) > 0 {
		return fmt.Sprintf("%f", val.Numbers[len(val.Numbers)-1]), nil
	}
	return "", fmt.Errorf("key not found: %s", key)
}

func getKeyAverage(key string, count int) (string, error) {
	val := values[key]
	if len(val.Numbers) > 0 {
		sum := 0.0
		for i := len(val.Numbers) - 1; i >= 0 && i >= len(val.Numbers)-count; i-- {
			sum += val.Numbers[i]
		}
		return fmt.Sprintf("%f", sum/float64(count)), nil
	}
	return "", fmt.Errorf("key not found: %s", key)
}

func getKeyTimestamp(key string) (time.Time, error) {
	val, ok := values[key]
	if ok {
		ts := val.Timestamp
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("key not found: %s", key)
}

func scanForExecutablesInPath(basePath string, uri string) ([]string, error) {
	var executables []string
	err := filepath.Walk(
		basePath,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			requestPath := basePath + "/" + uri + "/"
			scriptPath := filepath.Dir(path)
			//fmt.Println("For", path, "check if", scriptPath, "is a part of", requestPath)

			if !strings.HasPrefix(requestPath, scriptPath) {
				return nil
			}

			if info.Mode()&0111 != 0 {
				executables = append(executables, path)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return executables, nil
}

func scanForExecutables() []Executable {
	var executables []Executable
	err := filepath.Walk(
		config.HandlersPath,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			if info.Mode()&0111 != 0 {
				fmt.Println("Found executable:", path)
				description := readDescription(path)
				pathDirectory := filepath.Dir(path)
				pathDirectory = strings.TrimPrefix(pathDirectory, config.HandlersPath)
				pathFile := filepath.Base(path)
				executables = append(executables, Executable{Path: pathDirectory, Script: pathFile, Description: description})
			}
			return nil
		},
	)
	if err != nil {
		fmt.Println("Error scanning for executables:", err)
	}
	return executables
}

func readDescription(path string) string {
	var description string
	description = ""

	file, err := os.Open(path)
	if err != nil {
		return "error reading script file"
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var inComment bool
	inComment = false
	for i := 0; i < 20; i++ {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			break
		}
		if strings.Contains(line, "*/") && inComment {
			inComment = false
		}
		if inComment {
			if idx := strings.Index(line, "*"); idx != -1 {
				description += strings.TrimSpace(line[idx+1:])
			}
		}
		if strings.Contains(line, "/*") {
			inComment = true
		}

	}
	return description
}

func graphiteSend(key string, value string) error {
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", config.GraphiteHost, config.GraphitePort))
	conn.Write([]byte(fmt.Sprintf("%s %s %d\n", key, value, time.Now().Unix())))
	if err != nil {
		return err
	}
	defer conn.Close()
	return nil
}
