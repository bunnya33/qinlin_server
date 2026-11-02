package qinlin

import "context"

// Mock never performs network requests. Login accepts only the documented test code.
type Mock struct{}

func (Mock) SendSMS(context.Context, string) error { return nil }
func (Mock) Login(_ context.Context, _ string, code string) (Session, error) {
	if code != "123456" {
		return Session{}, &Error{"rejected"}
	}
	return Session{SessionID: "mock-session"}, nil
}
func (Mock) Doors(context.Context, Session) ([]Door, error) {
	return []Door{{CommunityID: "demo", CommunityName: "演示小区", DoorID: "south", Name: "南门（模拟）"}, {CommunityID: "demo", CommunityName: "演示小区", DoorID: "building", Name: "单元门（模拟）"}}, nil
}
func (Mock) Open(context.Context, Session, Door) error { return nil }
