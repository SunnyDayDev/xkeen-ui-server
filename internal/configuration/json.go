package configuration

import (
	"encoding/json"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

func parseJSON(raw json.RawMessage, location Issue) (any, *Issue) {
	value, issue := jsonvalue.Parse(raw)
	if issue == nil {
		return value, nil
	}
	location.Code, location.Path, location.Offset = issue.Code, issue.Path, issue.Offset
	return nil, &location
}

func appendPath(parent, part string) string { return jsonvalue.AppendPath(parent, part) }
