package hare

import (
	"github.com/spacemeshos/go-spacemesh/crypto"
	"github.com/spacemeshos/go-spacemesh/hare/config"
	"github.com/spacemeshos/go-spacemesh/p2p/service"
	"github.com/stretchr/testify/assert"
	"testing"
)

var cfg = config.DefaultConfig()

func generatePubKey(t *testing.T) crypto.PublicKey {
	_, pub, err := crypto.GenerateKeyPair()

	if err != nil {
		assert.Fail(t, "failed generating key")
		t.FailNow()
	}

	return pub
}

// test that a message to a specific set id is delivered by the broker
func TestConsensusProcess_StartTwice(t *testing.T) {
	sim := service.NewSimulator()
	n1 := sim.NewNode()

	broker := NewBroker(n1)
	s := NewEmptySet(cfg.SetSize)
	oracle := NewMockOracle()
	signing := NewMockSigning()

	proc := NewConsensusProcess(cfg, generatePubKey(t), *instanceId1, *s, oracle, signing, n1)
	broker.Register(proc)
	err := proc.Start()
	assert.Equal(t, nil, err)
	err = proc.Start()
	assert.Equal(t, "instance already started", err.Error())
}

func TestConsensusProcess_eventLoop(t *testing.T) {
	sim := service.NewSimulator()
	n1 := sim.NewNode()
	n2 := sim.NewNode()

	broker := NewBroker(n1)
	s := NewEmptySet(cfg.SetSize)
	oracle := NewMockOracle()
	signing := NewMockSigning()

	proc := NewConsensusProcess(cfg, generatePubKey(t), *instanceId1, *s, oracle, signing, n1)
	broker.Register(proc)
	go proc.eventLoop()
	n2.Broadcast(ProtoName, []byte{})

	proc.Close()
	<-proc.CloseChannel()
}

func TestConsensusProcess_handleMessage(t *testing.T) {
	sim := service.NewSimulator()
	n1 := sim.NewNode()

	broker := NewBroker(n1)
	s := NewEmptySet(cfg.SetSize)
	oracle := NewMockOracle()
	signing := NewMockSigning()

	proc := NewConsensusProcess(cfg, generatePubKey(t), *instanceId1, *s, oracle, signing, n1)
	broker.Register(proc)

	m := NewMessageBuilder().SetRoundCounter(0).SetInstanceId(*instanceId1).SetPubKey(generatePubKey(t)).Sign(proc.signing).Build()

	proc.handleMessage(m)
}

func TestConsensusProcess_nextRound(t *testing.T) {
	sim := service.NewSimulator()
	n1 := sim.NewNode()

	broker := NewBroker(n1)
	s := NewEmptySet(cfg.SetSize)
	oracle := NewMockOracle()
	signing := NewMockSigning()

	proc := NewConsensusProcess(cfg, generatePubKey(t), *instanceId1, *s, oracle, signing, n1)
	broker.Register(proc)

	proc.advanceToNextRound()
	assert.Equal(t, uint32(1), proc.k)
	proc.advanceToNextRound()
	assert.Equal(t, uint32(2), proc.k)
}

func generateConsensusProcess(t *testing.T) *ConsensusProcess {
	sim := service.NewSimulator()
	n1 := sim.NewNode()

	s := NewEmptySet(cfg.SetSize)
	oracle := NewMockOracle()
	signing := NewMockSigning()

	return NewConsensusProcess(cfg, generatePubKey(t), *instanceId1, *s, oracle, signing, n1)
}

func TestConsensusProcess_DoesMatchRound(t *testing.T) {
	s := NewEmptySet(cfg.SetSize)
	pub := generatePubKey(t)
	cp := generateConsensusProcess(t)

	msgType := make([]MessageType, 5, 5)
	msgType[0] = PreRound
	msgType[1] = Status
	msgType[2] = Proposal
	msgType[3] = Commit
	msgType[4] = Notify

	rounds := make([][4]bool, 5) // index=round
	rounds[0] = [4]bool{true, true, true, true}
	rounds[1] = [4]bool{true, false, false, false}
	rounds[2] = [4]bool{false, true, true, false}
	rounds[3] = [4]bool{false, false, true, false}
	rounds[4] = [4]bool{true, true, true, true}

	for j := 0; j < len(msgType); j++ {
		for i := 0; i < 4; i++ {
			builder := NewMessageBuilder()
			builder.SetType(msgType[j]).SetInstanceId(*instanceId1).SetRoundCounter(cp.k).SetKi(ki).SetValues(s)
			builder = builder.SetPubKey(pub).Sign(NewMockSigning())
			assert.Equal(t, rounds[j][i], cp.isContextuallyValid(builder.Build()))
			cp.advanceToNextRound()
		}
	}
}

func TestConsensusProcess_Id(t *testing.T) {
	proc := generateConsensusProcess(t)
	proc.instanceId = *instanceId1
	assert.Equal(t, instanceId1.Id(), proc.Id())
}

func TestNewConsensusProcess_AdvanceToNextRound(t *testing.T) {
	proc := generateConsensusProcess(t)
	k := proc.k
	proc.advanceToNextRound()
	assert.Equal(t, k+1, proc.k)
}

func TestConsensusProcess_CreateInbox(t *testing.T) {
	proc := generateConsensusProcess(t)
	proc.createInbox(100)
	assert.NotNil(t, proc.inbox)
	assert.Equal(t, 100, cap(proc.inbox))
}

func TestConsensusProcess_InitDefaultBuilder(t *testing.T) {
	proc := generateConsensusProcess(t)
	s := NewEmptySet(cfg.SetSize)
	s.Add(value1)
	builder := proc.initDefaultBuilder(s)
	assert.True(t, NewSet(builder.inner.Values).Equals(s))
	pub, err := crypto.NewPublicKey(builder.outer.PubKey)
	assert.Nil(t, err)
	assert.Equal(t, pub.Bytes(), proc.pubKey.Bytes())
	assert.Equal(t, builder.inner.K, proc.k)
	assert.Equal(t, builder.inner.Ki, proc.ki)
	assert.Equal(t, builder.inner.InstanceId, proc.instanceId.Bytes())
}

func TestConsensusProcess_buildNotify(t *testing.T) {
	proc := generateConsensusProcess(t)
	m := proc.buildNotifyMessage()
	assert.Equal(t, Notify, MessageType(m.Message.Type))
}