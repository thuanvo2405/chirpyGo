package main

import (
	"log"
	"net/http"
)

func main() {
	serveMux := http.NewServeMux()

	s := &http.Server{
		Addr:    ":8080",
		Handler: serveMux,
	}

	err := s.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}
