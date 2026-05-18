package billing

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)


func PaymentExistsByReference(ctx context.Context, db *mongo.Database, userID primitive.ObjectID, referenceCode string) bool {
	referenceCode = strings.TrimSpace(referenceCode)
	if referenceCode == "" {
		return false
	}
	n, err := db.Collection(collPayments).CountDocuments(ctx, bson.M{
		"user_id":          userID,
		"reference_code": referenceCode,
	})
	if err != nil {
		return false
	}
	return n > 0
}

func RecordPayment(ctx context.Context, db *mongo.Database, userID primitive.ObjectID, plan, referenceCode, source string, paidAt, planExpiresAt time.Time) error {
	doc := bson.M{
		"user_id":          userID,
		"plan":             strings.ToLower(strings.TrimSpace(plan)),
		"reference_code":   strings.TrimSpace(referenceCode),
		"paid_at":          paidAt,
		"plan_expires_at": planExpiresAt,
		"source":           strings.TrimSpace(source),
		"created_at":       time.Now().UTC(),
	}
	if doc["reference_code"] == "" {
		delete(doc, "reference_code")
	}
	_, err := db.Collection(collPayments).InsertOne(ctx, doc)
	return err
}

func InsertSubscriptionActivatedNotification(ctx context.Context, db *mongo.Database, userID primitive.ObjectID, plan string, expiresAt time.Time, bankRef string) error {
	ref := strings.TrimSpace(bankRef)
	body := strings.Builder{}
	body.WriteString("Gói ")
	body.WriteString(strings.ToUpper(plan))
	body.WriteString(" đã được kích hoạt. Hết hạn: ")
	body.WriteString(expiresAt.In(time.Local).Format("02/01/2006 15:04"))
	if ref != "" {
		body.WriteString(". Mã tham chiếu: ")
		body.WriteString(ref)
	}

	doc := bson.M{
		"user_id":    userID,
		"kind":       "subscription_activated",
		"title":      "Đăng ký gói thành công",
		"body":       body.String(),
		"read":       false,
		"created_at": time.Now().UTC(),
	}
	_, err := db.Collection(collNotifications).InsertOne(ctx, doc)
	return err
}
func InsertSubscriptionCancelledNotification(ctx context.Context, db *mongo.Database, userID primitive.ObjectID, plan string) error {
	doc := bson.M{
		"user_id":    userID,
		"kind":       "subscription_cancelled",
		"title":      "Đã hủy gói cước",
		"body":       "Bạn đã hủy gói " + strings.ToUpper(plan) + ". Các tính năng Premium sẽ bị giới hạn sau khi hết hạn gói.",
		"read":       false,
		"created_at": time.Now().UTC(),
	}
	_, err := db.Collection(collNotifications).InsertOne(ctx, doc)
	return err
}
