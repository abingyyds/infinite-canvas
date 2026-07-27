package handler

import (
	"net/http"

	"github.com/basketikun/infinite-canvas/model"
	"github.com/basketikun/infinite-canvas/service"
)

func Prompts(w http.ResponseWriter, r *http.Request) {
	result, err := service.ListPrompts(parseQuery(r))
	if err != nil {
		FailError(w, err)
		return
	}
	OK(w, result)
}

// PromptsSource 按前端提示词来源的约定返回裸数组，使站内提示词库可作为一个内置来源加载。
func PromptsSource(w http.ResponseWriter, r *http.Request) {
	items := []model.Prompt{}
	for page := 1; ; page++ {
		result, err := service.ListPrompts(model.Query{Page: page, PageSize: model.MaxPageSize})
		if err != nil {
			FailError(w, err)
			return
		}
		items = append(items, result.Items...)
		if len(result.Items) == 0 || len(items) >= result.Total {
			break
		}
	}
	writeJSONStatus(w, http.StatusOK, items)
}
