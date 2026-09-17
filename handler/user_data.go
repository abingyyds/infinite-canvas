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

func SaveUserCanvasProjects(w http.ResponseWriter, r *http.Request) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		FailStatus(w, http.StatusUnauthorized, "未登录")
		return
	}
	var patch service.CanvasProjectsPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		Fail(w, "参数错误")
		return
	}
	result, err := service.SaveUserCanvasProjects(user, patch)
	if err != nil {
		FailError(w, err)
		return
	}
	if len(result.Conflicts) > 0 {
		// 409 而不是 200：客户端要靠状态码分流去做合并重试，HTTP 层也才看得见冲突率。
		// 没冲突的画布这次已经写进去了，result 里照样带着它们的新版本号。
		writeJSONStatus(w, http.StatusConflict, response{Code: 1, Data: result, Msg: "画布已在别处更新"})
		return
	}
	OK(w, result)
}
