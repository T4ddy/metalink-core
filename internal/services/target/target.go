package target

type Target struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Speed     float32     `json:"speed"`
	TargetLat float32     `json:"target_lat"`
	TargetLng float32     `json:"target_lng"`
	Route     string      `json:"route"`
	State     TargetState `json:"state"`
}
