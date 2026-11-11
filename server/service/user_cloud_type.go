package service

import (
	"strings"

	"kvm_console/model"
)

// GetUserCloudType 返回用户的云类型（elastic / lightweight）；查不到用户时返回 "elastic"。
func GetUserCloudType(username string) string {
	name := strings.TrimSpace(username)
	if name == "" || model.DB == nil {
		return CloudTypeElastic
	}
	var user model.User
	if err := model.DB.Select("cloud_type").Where("username = ?", name).First(&user).Error; err != nil {
		return CloudTypeElastic
	}
	return NormalizeCloudType(user.CloudType)
}
