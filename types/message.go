package types

type MessageType int

const (
	MessageTypeText MessageType = iota
	MessageTypePhoto
	MessageTypeVideo
	MessageTypeDocument
	MessageTypeVoice
	MessageTypeAudio
	MessageTypePoll
	MessageTypeStory
)
