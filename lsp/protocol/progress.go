package protocol

type WorkDoneProgressCreateParams struct {
	Token string `json:"token"`
}
type ProgressParams struct {
	Token string `json:"token"`
	Value any    `json:"value"`
}
type WorkDoneProgressBegin struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
}
type WorkDoneProgressEnd struct {
	Kind string `json:"kind"`
}
