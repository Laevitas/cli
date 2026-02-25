package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/guptarohit/asciigraph"
)

const (
	chartWidth  = 60
	chartHeight = 15
)

// chartColumnMap maps API endpoint keywords to the column name to plot.
// The key is a substring that appears in the endpoint path.
var chartColumnMap = map[string]struct {
	column  string
	caption string
}{
	"ohlcv":         {column: "close", caption: "Close Price"},
	"funding":       {column: "funding_rate_close", caption: "Funding Rate"},
	"open-interest": {column: "oi_close", caption: "Open Interest"},
	"basis":         {column: "annualized_carry", caption: "Annualized Carry"},
	"volume":        {column: "volume", caption: "Volume"},
}

// ChartableEndpoint returns the column name and caption to chart for a
// given API endpoint, or empty strings if the endpoint is not chartable.
func ChartableEndpoint(endpoint string) (column, caption string) {
	for key, info := range chartColumnMap {
		if strings.Contains(endpoint, key) {
			return info.column, info.caption
		}
	}
	return "", ""
}

// RenderChart extracts a numeric series from raw JSON data and renders
// an ASCII line chart. It writes the chart to w.
// Returns silently if the data doesn't contain the target column or has
// fewer than 2 data points.
func RenderChart(w io.Writer, data []byte, column, caption string) {
	values := extractSeries(data, column)
	if len(values) < 2 {
		return
	}

	// Determine chart color based on trend
	color := asciigraph.Cyan
	if len(values) >= 2 {
		if values[len(values)-1] > values[0] {
			color = asciigraph.Green
		} else if values[len(values)-1] < values[0] {
			color = asciigraph.Red
		}
	}

	graph := asciigraph.Plot(values,
		asciigraph.Width(chartWidth),
		asciigraph.Height(chartHeight),
		asciigraph.Caption(caption),
		asciigraph.SeriesColors(color),
		asciigraph.CaptionColor(asciigraph.White),
		asciigraph.AxisColor(asciigraph.DarkGray),
		asciigraph.LabelColor(asciigraph.DarkGray),
	)

	fmt.Fprintln(w)
	fmt.Fprintln(w, graph)
}

// extractSeries parses raw JSON (expected to be an array of objects)
// and extracts float64 values from the given column name.
func extractSeries(data []byte, column string) []float64 {
	// Try parsing as array of objects directly
	var records []map[string]interface{}
	if err := json.Unmarshal(data, &records); err != nil {
		// Try unwrapping from { "data": [...] }
		var wrapper struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &wrapper) != nil || wrapper.Data == nil {
			return nil
		}
		if json.Unmarshal(wrapper.Data, &records) != nil {
			return nil
		}
	}

	values := make([]float64, 0, len(records))
	for _, rec := range records {
		v, ok := rec[column]
		if !ok {
			continue
		}
		switch val := v.(type) {
		case float64:
			values = append(values, val)
		case string:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				values = append(values, f)
			}
		}
	}
	return values
}
