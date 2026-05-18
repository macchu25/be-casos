package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// users
type User struct {
	ID                 primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name               string             `bson:"name" json:"name"`
	Email              string             `bson:"email" json:"email"`
	Phone              string             `bson:"phone" json:"phone"`
	Role               string             `bson:"role" json:"role"`
	PasswordHash       string             `bson:"password_hash" json:"-"`
	Provider           string             `bson:"provider" json:"provider"`      // "google" or "facebook"
	ProviderID         string             `bson:"provider_id" json:"provider_id"` // ID từ MXH
	SubscriptionPlan   string             `bson:"subscription_plan" json:"subscription_plan"` // "free", "starter", "creator", "pro", "scale"
	SubscriptionStatus string             `bson:"subscription_status" json:"subscription_status"` // "active", "canceled", "past_due"
	PlanExpiresAt      *time.Time         `bson:"plan_expires_at,omitempty" json:"plan_expires_at"`
	CreatedAt          time.Time          `bson:"created_at" json:"created_at"`
}

// cpr_guides
type CPRGuide struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Title     string             `bson:"title" json:"title"`
	Steps     []string           `bson:"steps" json:"steps"`
	AudioURL  string             `bson:"audio_url" json:"audio_url"`
	Language  string             `bson:"language" json:"language"`
	IsActive  bool               `bson:"is_active" json:"is_active"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

// health_profiles
type HealthProfile struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID           primitive.ObjectID `bson:"user_id" json:"user_id"`
	BloodType        string             `bson:"blood_type" json:"blood_type"`
	Conditions       []string           `bson:"conditions" json:"conditions"`
	Medications      []string           `bson:"medications" json:"medications"`
	EmergencyContact string             `bson:"emergency_contact" json:"emergency_contact"`
	UpdatedAt        time.Time          `bson:"updated_at" json:"updated_at"`
}

// cameras
type Camera struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name      string             `bson:"name" json:"name"`
	RTSPURL   string             `bson:"rtsp_url" json:"rtsp_url"`
	Location  string             `bson:"location" json:"location"`
	Status    string             `bson:"status" json:"status"`
	UserID    primitive.ObjectID `bson:"user_id,omitempty" json:"user_id"` // Ràng buộc người dùng
	CreatedBy primitive.ObjectID `bson:"created_by,omitempty" json:"created_by"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
}

// events
type Event struct {
	ID              primitive.ObjectID     `bson:"_id,omitempty" json:"id"`
	CameraID        primitive.ObjectID     `bson:"camera_id" json:"camera_id"`
	UserID          primitive.ObjectID     `bson:"user_id,omitempty" json:"user_id"` // Phân quyền xem
	Type            string                 `bson:"type" json:"type"`
	ConfidenceScore float32                `bson:"confidence_score" json:"confidence_score"`
	Status          string                 `bson:"status" json:"status"`
	ClipURL         string                 `bson:"clip_url" json:"clip_url"`
	PoseData        map[string]interface{} `bson:"pose_data" json:"pose_data"`
	DetectedAt      time.Time              `bson:"detected_at" json:"detected_at"`
	ResolvedAt      *time.Time             `bson:"resolved_at,omitempty" json:"resolved_at"`
}

// alerts
type Alert struct {
	ID           primitive.ObjectID     `bson:"_id,omitempty" json:"id"`
	EventID      primitive.ObjectID     `bson:"event_id" json:"event_id"`
	CameraID     primitive.ObjectID     `bson:"camera_id" json:"camera_id"`
	Channel      string                 `bson:"channel" json:"channel"`
	Recipient    string                 `bson:"recipient" json:"recipient"`
	Status       string                 `bson:"status" json:"status"`
	ResponseData map[string]interface{} `bson:"response_data" json:"response_data"`
	SentAt       time.Time              `bson:"sent_at" json:"sent_at"`
	AckedAt      *time.Time             `bson:"acked_at,omitempty" json:"acked_at"`
}

// ai_models
type AIModel struct {
	ID         primitive.ObjectID     `bson:"_id,omitempty" json:"id"`
	Name       string                 `bson:"name" json:"name"`
	Version    string                 `bson:"version" json:"version"`
	FilePath   string                 `bson:"file_path" json:"file_path"`
	Accuracy   float32                `bson:"accuracy" json:"accuracy"`
	Status     string                 `bson:"status" json:"status"`
	Metrics    map[string]interface{} `bson:"metrics" json:"metrics"`
	TrainedAt  time.Time              `bson:"trained_at" json:"trained_at"`
	DeployedAt *time.Time             `bson:"deployed_at,omitempty" json:"deployed_at"`
}
