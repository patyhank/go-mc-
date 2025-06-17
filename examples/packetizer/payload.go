package packetizer

import (
	"github.com/Tnze/go-mc/chat"
	"github.com/Tnze/go-mc/nbt"
	"github.com/google/uuid"
)

// ExampleTypeFull full example for codec generator
//
//codec:gen
type ExampleTypeFull struct {
	PlayerID         int32 `mc:"VarInt"`
	PlayerName       string
	UUID             uuid.UUID      `mc:"UUID"`
	ResourceLocation string         `mc:"Identifier"`
	Data             nbt.RawMessage `mc:"NBT"`
	ByteData         []byte         `mc:"ByteArray"`
	Health           float32
	Balance          float64
	Message          chat.Message
	SentMessages     []chat.Message
}
