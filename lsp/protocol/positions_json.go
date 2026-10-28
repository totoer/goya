package protocol

import (
	"encoding/json"
	"fmt"
)

func (p *Position) UnmarshalJSON(data []byte) error {
	var wire struct {
		Line      *uint32 `json:"line"`
		Character *uint32 `json:"character"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Line == nil || wire.Character == nil {
		return fmt.Errorf("position requires line and character")
	}
	p.Line = *wire.Line
	p.Character = *wire.Character
	return nil
}
func (r *Range) UnmarshalJSON(data []byte) error {
	var wire struct {
		Start *Position `json:"start"`
		End   *Position `json:"end"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Start == nil || wire.End == nil {
		return fmt.Errorf("range requires start and end")
	}
	r.Start = *wire.Start
	r.End = *wire.End
	return nil
}
func (c *ContentChange) UnmarshalJSON(data []byte) error {
	var wire struct {
		Range *Range  `json:"range"`
		Text  *string `json:"text"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Text == nil {
		return fmt.Errorf("content change requires text")
	}
	c.Range = wire.Range
	c.Text = *wire.Text
	return nil
}
