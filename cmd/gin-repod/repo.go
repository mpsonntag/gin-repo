package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/mux"

	"github.com/G-Node/gin-repo/git"
	"github.com/G-Node/gin-repo/wire"
	"regexp"
)

var nameChecker *regexp.Regexp

func init() {
	nameChecker = regexp.MustCompile("^[a-zA-Z0-9-_.]{3,}$")
}

func checkName(name string) bool {
	return nameChecker.MatchString(name)
}

func createRepo(w http.ResponseWriter, r *http.Request) {
	log.Printf("createRepo: %s @ %s", r.Method, r.URL.String())

	decoder := json.NewDecoder(r.Body)
	var creat wire.CreateRepo
	err := decoder.Decode(&creat)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(os.Stderr, "Error precessing request: %v", err)
		return
	} else if creat.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(os.Stderr, "Error precessing request: name missing")
		return
	} else if !checkName(creat.Name) {
		http.Error(w, "Invalid repository name", http.StatusBadRequest)
		fmt.Fprintf(os.Stderr, "Error precessing request: invalid name: %q", creat.Name)
		return
	}

	vars := mux.Vars(r)
	user := vars["user"]

	path := translatePath(creat.Name, user)

	repo, err := git.InitBareRepository(path)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	// ignore error, because we created the repo
	//  which is more important
	repo.WriteDescription(creat.Description)

	wr := wire.Repo{Name: creat.Name, Description: repo.ReadDescription()}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	js := json.NewEncoder(w)
	err = js.Encode(wr)

	if err != nil {
		log.Printf("Error while encoding, status already sent. oh oh.")
	}
}