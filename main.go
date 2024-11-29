package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
)

func init() {
	values = make(map[string][]float64)
}

type Config struct {
	HandlersPath string
}

var config Config
var values map[string][]float64

func init() {
	config = Config{
		HandlersPath: "/var/www/handlers",
	}
}

func shutdown() {
	fmt.Println("Shutting down server...")
	fmt.Println("Writing values to disk...")

	file, err := os.Create("values.json")
	if err != nil {
		fmt.Println("Error creating file:", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(values); err != nil {
		fmt.Println("Error encoding values to JSON:", err)
	}
}

func main() {
	defer shutdown()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		shutdown()
		os.Exit(1)
	}()

	fmt.Println("Loading values...")
	file, err := os.Open("values.json")
	if err == nil {
		defer file.Close()
		decoder := json.NewDecoder(file)
		if err := decoder.Decode(&values); err != nil {
			fmt.Println("Error decoding values from JSON:", err)
		}
	} else if !os.IsNotExist(err) {
		fmt.Println("Error opening values.json:", err)
	}

	fmt.Println("Starting server...")

	http.HandleFunc("/set/", setHandler)
	http.HandleFunc("/get/", getHandler)
	fmt.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Println("Error starting server:", err)
	}
}

func getHandler(w http.ResponseWriter, r *http.Request) {
	var value float64
	var err error
	var query url.Values
	var paramAverage string

	fmt.Println("Received request for:", r.URL.Path)
	uri := r.URL.Path
	uri = strings.TrimPrefix(uri, "/get/")
	uri = strings.ReplaceAll(uri, "/", ".")
	fmt.Println("Modified URI:", uri)

	w.WriteHeader(http.StatusOK)

	query = r.URL.Query()
	paramAverage = query.Get("average")
	if paramAverage == "" {
		value, err = getKeyLatest(uri)
	} else {
		var count int
		count, err = strconv.Atoi(paramAverage)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		value, err = getKeyAverage(uri, count)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Write([]byte(fmt.Sprintf("%f", value)))
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
	} else {
		w.Write([]byte("Set: " + uriDotted + " to '" + paramValue + "'"))
		fmt.Println("Query parameter 'value' was set to:", paramValue)
		setKey(uriDotted, paramValue)
	}

	executables, err := scanForExecutables(config.HandlersPath, uri)
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
		var val float64
		var err error
		val, err = getKeyAverage(uriDotted, 3)
		os.Setenv("EVENT_VALUE_AVG_3", fmt.Sprintf("%f", val))
		val, err = getKeyAverage(uriDotted, 5)
		os.Setenv("EVENT_VALUE_AVG_5", fmt.Sprintf("%f", val))
		val, err = getKeyAverage(uriDotted, 10)
		os.Setenv("EVENT_VALUE_AVG_10", fmt.Sprintf("%f", val))
		if err != nil {
			fmt.Println("Error getting average value:", err)
		}

		if err := exec.Command(executable).Run(); err != nil {
			fmt.Println(`Error executing:`, err)
		}
	}
}

func setKey(key string, value string) error {
	val, err := strconv.ParseFloat(value, 64)
	if err != nil {
		fmt.Println("Invalid value for 'value':", value)
		return err
	}
	values[key] = append(values[key], val)
	if len(values[key]) > 10 {
		values[key] = values[key][1:]
	}
	return nil
}

func getKeyLatest(key string) (float64, error) {
	if vals, ok := values[key]; ok && len(vals) > 0 {
		return vals[len(vals)-1], nil
	}
	return 0, fmt.Errorf("key not found: %s", key)
}

func getKeyAverage(key string, count int) (float64, error) {
	if vals, ok := values[key]; ok && len(vals) > 0 {
		sum := 0.0
		for i := len(vals) - 1; i >= 0 && i >= len(vals)-count; i-- {
			sum += vals[i]
		}
		return sum / float64(count), nil
	}
	return 0, fmt.Errorf("key not found: %s", key)
}

func scanForExecutables(basePath string, uri string) ([]string, error) {
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
			fmt.Println("For", path, "check if", scriptPath, "is a part of", requestPath)

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
