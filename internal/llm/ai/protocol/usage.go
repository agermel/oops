package protocol

type Usage struct {
	Input        int        `json:"input"`
	Output       int        `json:"output"`
	CacheRead    int        `json:"cacheRead"`
	CacheWrite   int        `json:"cacheWrite"`
	CacheWrite1h *int       `json:"cacheWrite1h,omitempty"`
	Reasoning    *int       `json:"reasoning,omitempty"`
	TotalTokens  int        `json:"totalTokens"`
	Cost         *UsageCost `json:"cost,omitempty"`
}

type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}
