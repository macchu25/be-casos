package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type CameraState struct {
	SuspectStart   time.Time `json:"suspect_start"`
	LastAlert      time.Time `json:"last_alert"`
	LocalAlertSent bool      `json:"local_alert_sent"`
	AlertPaused    bool      `json:"alert_paused"`
}

type StateStorage interface {
	Get(ctx context.Context, camID primitive.ObjectID) (*CameraState, error)
	Set(ctx context.Context, camID primitive.ObjectID, state *CameraState) error
	Delete(ctx context.Context, camID primitive.ObjectID) error
}

// RedisStorage implements StateStorage using Redis
type RedisStorage struct {
	client *redis.Client
}

func NewRedisStorage(url string) *RedisStorage {
	rdb := redis.NewClient(&redis.Options{
		Addr: url,
	})
	return &RedisStorage{client: rdb}
}

func (r *RedisStorage) Get(ctx context.Context, camID primitive.ObjectID) (*CameraState, error) {
	key := fmt.Sprintf("cam_state:%s", camID.Hex())
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var state CameraState
	err = json.Unmarshal([]byte(val), &state)
	return &state, err
}

func (r *RedisStorage) Set(ctx context.Context, camID primitive.ObjectID, state *CameraState) error {
	key := fmt.Sprintf("cam_state:%s", camID.Hex())
	data, _ := json.Marshal(state)
	return r.client.Set(ctx, key, data, 24*time.Hour).Err()
}

func (r *RedisStorage) Delete(ctx context.Context, camID primitive.ObjectID) error {
	key := fmt.Sprintf("cam_state:%s", camID.Hex())
	return r.client.Del(ctx, key).Err()
}

// MemoryStorage implements StateStorage using an in-memory map (Fallback)
type MemoryStorage struct {
	states map[primitive.ObjectID]*CameraState
	mu     sync.RWMutex
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{states: make(map[primitive.ObjectID]*CameraState)}
}

func (m *MemoryStorage) Get(ctx context.Context, camID primitive.ObjectID) (*CameraState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.states[camID], nil
}

func (m *MemoryStorage) Set(ctx context.Context, camID primitive.ObjectID, state *CameraState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[camID] = state
	return nil
}

func (m *MemoryStorage) Delete(ctx context.Context, camID primitive.ObjectID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, camID)
	return nil
}
