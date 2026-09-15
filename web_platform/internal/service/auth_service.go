package service

import (
	"errors"
	"strings"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/jwt"

	"gorm.io/gorm"
)

type AuthService struct {
	bridge *RelayBridgeService
}

func NewAuthService() *AuthService {
	return &AuthService{
		bridge: NewRelayBridgeService(),
	}
}

func (s *AuthService) Register(username, email, password string) (*model.User, string, error) {
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)
	password = strings.TrimSpace(password)

	if username == "" || password == "" {
		return nil, "", errors.New("用户名和密码不能为空")
	}

	if len(password) < 6 {
		return nil, "", errors.New("密码长度不能小于 6 位")
	}

	db := database.DB

	// 检查用户名是否已存在
	var count int64
	db.Model(&model.User{}).Where("username = ?", username).Count(&count)
	if count > 0 {
		return nil, "", errors.New("该用户名已被注册")
	}

	if email != "" {
		db.Model(&model.User{}).Where("email = ?", email).Count(&count)
		if count > 0 {
			return nil, "", errors.New("该邮箱已被绑定")
		}
	}

	user := model.User{
		Username: username,
		Email:    email,
		Role:     "user",
		Status:   "active",
	}
	if err := user.SetPassword(password); err != nil {
		return nil, "", err
	}

	if err := db.Create(&user).Error; err != nil {
		return nil, "", err
	}

	// 自动生成 JWT Token
	cfg := config.GlobalConfig
	token, err := jwt.GenerateToken(user.ID, user.Username, user.Role, cfg.Server.JWTSecret, cfg.Server.TokenExpireHours)
	if err != nil {
		return nil, "", err
	}

	// 桥接同步该用户至 18444 Go Relay 服务端
	if s.bridge != nil {
		_ = s.bridge.SyncUserToRelay(user.Username, password, "web_platform registration")
	}

	return &user, token, nil
}

func (s *AuthService) Login(usernameOrEmail, password string) (*model.User, string, error) {
	usernameOrEmail = strings.TrimSpace(usernameOrEmail)
	password = strings.TrimSpace(password)

	if usernameOrEmail == "" || password == "" {
		return nil, "", errors.New("用户名或密码不能为空")
	}

	db := database.DB

	var user model.User
	err := db.Preload("Plan").Where("username = ? OR email = ?", usernameOrEmail, usernameOrEmail).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", errors.New("账号或密码错误")
		}
		return nil, "", err
	}

	if user.Status == "disabled" {
		return nil, "", errors.New("该账号已被禁用，请联系管理员")
	}

	if !user.CheckPassword(password) {
		return nil, "", errors.New("账号或密码错误")
	}

	// 桥接同步该用户至 18444 Go Relay 服务端(确保历史存量用户激活)
	if s.bridge != nil {
		_ = s.bridge.SyncUserToRelay(user.Username, password, "web_platform login sync")
	}

	cfg := config.GlobalConfig
	token, err := jwt.GenerateToken(user.ID, user.Username, user.Role, cfg.Server.JWTSecret, cfg.Server.TokenExpireHours)
	if err != nil {
		return nil, "", err
	}

	return &user, token, nil
}

func (s *AuthService) GetProfile(userID uint) (*model.User, error) {
	var user model.User
	err := database.DB.Preload("Plan").First(&user, userID).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *AuthService) ChangePassword(userID uint, oldPwd, newPwd string) error {
	var user model.User
	if err := database.DB.First(&user, userID).Error; err != nil {
		return err
	}

	if !user.CheckPassword(oldPwd) {
		return errors.New("原密码错误")
	}

	if len(newPwd) < 6 {
		return errors.New("新密码长度不能小于 6 位")
	}

	if err := user.SetPassword(newPwd); err != nil {
		return err
	}

	return database.DB.Save(&user).Error
}
