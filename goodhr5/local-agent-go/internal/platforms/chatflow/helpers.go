// Package chatflow 本文件提供包内使用的 map 取值和姓名清洗辅助函数。
package chatflow

import (
	"strings"
)

// normalizeName 清理姓名中的空白和常见称谓后缀，用于聊天姓名与候选人姓名比对。
func normalizeName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Join(strings.Fields(value), "")
	for _, suffix := range []string{"先生", "女士"} {
		value = strings.TrimSuffix(value, suffix)
	}
	return value
}

// stringFromMap 从 map 中读取字符串字段。
// item 为来源 map，key 为字段名。
func stringFromMap(item map[string]any, key string) string {
	if item == nil {
		return ""
	}
	if value, ok := item[key].(string); ok {
		return value
	}
	return ""
}

// mapFromAny 把 any 值安全转换为 map。
// value 为来源值，转换失败时返回空 map。
func mapFromAny(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	return map[string]any{}
}

// mapList 把 any 值安全转换为 map 列表。
// value 为来源值，转换失败时返回空列表。
func mapList(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, mapFromAny(item))
	}
	return result
}

// workerData 从 Worker 响应中取出 data 字段。
// result 为 Worker 响应，key 为要取的字段名。
func workerData(result map[string]any, key string) any {
	if result == nil {
		return nil
	}
	data := mapFromAny(result["data"])
	if data[key] != nil {
		return data[key]
	}
	return result[key]
}

// firstNonEmpty 返回第一个非空字符串。
// values 为候选字符串列表。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
