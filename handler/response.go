package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/basketikun/infinite-canvas/model"
)

type response struct {
	Code int    `json:"code"`
	Data any    `json:"data"`
	Msg  string `json:"msg"`
}

func OK(w http.ResponseWriter, data any) {
	writeJSONStatus(w, http.StatusOK, response{Code: 0, Data: data, Msg: "ok"})
}

func Fail(w http.ResponseWriter, msg string) {
	FailStatus(w, http.StatusOK, msg)
}

func FailStatus(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, response{Code: 1, Data: nil, Msg: msg})
}

func FailError(w http.ResponseWriter, err error) {
	log.Printf("request failed: %v", err)
	if safe, ok := err.(interface{ SafeMessage() string }); ok {
		Fail(w, safe.SafeMessage())
		return
	}
	Fail(w, "操作失败")
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func parseQuery(r *http.Request) model.Query {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("pageSize"))
	return model.Query{
		Keyword:  q.Get("keyword"),
		Tags:     q["tag"],
		Category: q.Get("category"),
		Type:     q.Get("type"),
		Page:     page,
		PageSize: pageSize,
	}
}
