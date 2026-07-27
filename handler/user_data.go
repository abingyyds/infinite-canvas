package handler

import (
	"encoding/json"
	"net/http"

	"github.com/basketikun/infinite-canvas/service"
)

type saveUserDataRequest struct {
	Data json.RawMessage `json:"data"`
}

func UserDataSnapshot(w http.ResponseWriter, r *http.Request, domain string) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		FailStatus(w, http.StatusUnauthorized, "未登录")
		return
	}
	result, err := service.GetUserDataSnapshot(user, domain)
	if err != nil {
		FailError(w, err)
		return
	}
	OK(w, result)
}

func SaveUserDataSnapshot(w http.ResponseWriter, r *http.Request, domain string) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		FailStatus(w, http.StatusUnauthorized, "未登录")
		return
	}
	var request saveUserDataRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		Fail(w, "参数错误")
		return
	}
	result, err := service.SaveUserDataSnapshot(user, domain, request.Data)
	if err != nil {
		FailError(w, err)
		return
	}
	OK(w, result)
}
