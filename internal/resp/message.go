package resp

type TypeTag byte

const (
	TypeSimpleString TypeTag = '+'
	TypeError        TypeTag = '-'
	TypeInteger      TypeTag = ':'
	TypeBulkString   TypeTag = '$'
	TypeArray        TypeTag = '*'
)

type Message interface {
	TypeTag() TypeTag
	Bytes() []byte
}

type SimpleString struct {
	Value string
	raw   []byte
}

func (m *SimpleString) TypeTag() TypeTag { return TypeSimpleString }
func (m *SimpleString) Bytes() []byte    { return m.raw }

type Error struct {
	Value string
	raw   []byte
}

func (m *Error) TypeTag() TypeTag { return TypeError }
func (m *Error) Bytes() []byte    { return m.raw }

type Integer struct {
	Value int64
	raw   []byte
}

func (m *Integer) TypeTag() TypeTag { return TypeInteger }
func (m *Integer) Bytes() []byte    { return m.raw }

type BulkString struct {
	Value string
	raw   []byte
}

func (m *BulkString) TypeTag() TypeTag { return TypeBulkString }
func (m *BulkString) Bytes() []byte    { return m.raw }

type NullBulkString struct {
	raw []byte
}

func (m *NullBulkString) TypeTag() TypeTag { return TypeBulkString }
func (m *NullBulkString) Bytes() []byte    { return m.raw }

type Array struct {
	Items []Message
	raw   []byte
}

func (m *Array) TypeTag() TypeTag { return TypeArray }
func (m *Array) Bytes() []byte    { return m.raw }
