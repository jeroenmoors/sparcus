package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-redis/redis/v8"
)

var rdb *redis.Client

func init() {
	rdb = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
}

type Config struct {
	RedisAddr    string
	HandlersPath string
}

var config Config
var values map[string]float64

func init() {
	config = Config{
		RedisAddr:    "localhost:6379",
		HandlersPath: "/var/www/handlers",
	}

	rdb = redis.NewClient(&redis.Options{
		Addr: config.RedisAddr,
	})
}

func getHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received request for:", r.URL.Path)
	uri := r.URL.Path
	uri = strings.TrimPrefix(uri, "/get/")
	uri = strings.ReplaceAll(uri, "/", ".")
	fmt.Println("Modified URI:", uri)

	w.WriteHeader(http.StatusOK)

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

		val, err := strconv.ParseFloat(paramValue, 64)
		if err != nil {
			w.Write([]byte("Set: " + uriDotted + " invalid value provided"))
			fmt.Println("Invalid value for 'value':", paramValue)
			return
		}
		values[uriDotted] = val
	}

	executables, err := scanForExecutables(config.HandlersPath, uri)
	if err != nil {
		fmt.Println("Error scanning for executables:", err)
	} else {
		fmt.Println("Found executables:", executables)
	}

}

func main() {
	fmt.Println("Starting server...")

	fmt.Println("Connecting to Redis...")

	http.HandleFunc("/set/", setHandler)
	http.HandleFunc("/get/", getHandler)
	fmt.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Println("Error starting server:", err)
	}

}

func setKey(key string, value string) error {
	ctx := context.Background()
	err := rdb.Set(ctx, key, value, 0).Err()
	if err != nil {
		return err
	}
	return nil
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
