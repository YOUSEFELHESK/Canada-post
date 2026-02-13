package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	address "bitbucket.org/lexmodo/proto/address"
	labels "bitbucket.org/lexmodo/proto/labels"
	money "bitbucket.org/lexmodo/proto/money"
	"lexmodo-plugin/config"
)

var errRedisNil = errors.New("redis: nil")

type redisClient struct {
	addr     string
	password string
	db       int
	timeout  time.Duration
}

func newRedisClient(cfg config.RedisConfig) *redisClient {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil
	}
	return &redisClient{
		addr:     addr,
		password: strings.TrimSpace(cfg.Password),
		db:       cfg.DB,
		timeout:  3 * time.Second,
	}
}

func (c *redisClient) set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c == nil {
		return errors.New("redis client not configured")
	}
	args := []string{"SET", key, string(value)}
	if ttl > 0 {
		args = append(args, "EX", strconv.FormatInt(int64(ttl.Seconds()), 10))
	}
	_, err := c.do(ctx, args...)
	return err
}

func (c *redisClient) get(ctx context.Context, key string) ([]byte, error) {
	if c == nil {
		return nil, errors.New("redis client not configured")
	}
	reply, err := c.do(ctx, "GET", key)
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return nil, errRedisNil
	}
	if data, ok := reply.([]byte); ok {
		return data, nil
	}
	if str, ok := reply.(string); ok {
		return []byte(str), nil
	}
	return nil, fmt.Errorf("unexpected redis reply type %T", reply)
}

func (c *redisClient) do(ctx context.Context, args ...string) (any, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	if c.password != "" {
		if err := writeCommand(writer, "AUTH", c.password); err != nil {
			return nil, err
		}
		if _, err := readReply(reader); err != nil {
			return nil, err
		}
	}
	if c.db > 0 {
		if err := writeCommand(writer, "SELECT", strconv.Itoa(c.db)); err != nil {
			return nil, err
		}
		if _, err := readReply(reader); err != nil {
			return nil, err
		}
	}

	if err := writeCommand(writer, args...); err != nil {
		return nil, err
	}
	return readReply(reader)
}

func (c *redisClient) dial(ctx context.Context) (net.Conn, error) {
	dialer := net.Dialer{Timeout: c.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(c.timeout))
	}
	return conn, nil
}

func writeCommand(w *bufio.Writer, args ...string) error {
	if len(args) == 0 {
		return errors.New("redis command missing arguments")
	}
	if _, err := w.WriteString(fmt.Sprintf("*%d\r\n", len(args))); err != nil {
		return err
	}
	for _, arg := range args {
		if _, err := w.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg)); err != nil {
			return err
		}
	}
	return w.Flush()
}

func readReply(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch prefix {
	case '+':
		line, err := readLine(r)
		return line, err
	case '-':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		return nil, errors.New(line)
	case ':':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		value, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return nil, err
		}
		return value, nil
	case '$':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if size < 0 {
			return nil, nil
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		if _, err := r.ReadByte(); err != nil {
			return nil, err
		}
		if _, err := r.ReadByte(); err != nil {
			return nil, err
		}
		return buf, nil
	case '*':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		count, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if count < 0 {
			return nil, nil
		}
		values := make([]any, 0, count)
		for i := 0; i < count; i++ {
			value, err := readReply(r)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unexpected redis reply prefix %q", prefix)
	}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

type RateSnapshot struct {
	RateID        string                `json:"rate_id"`
	ServiceCode   string                `json:"service_code"`
	ServiceName   string                `json:"service_name"`
	PriceCents    int64                 `json:"price_cents"`
	CurrencyCode  string                `json:"currency_code"`
	RateToCad     float64               `json:"rate_to_cad"`
	DeliveryDate  string                `json:"delivery_date"`
	Signature     string                `json:"signature"`
	CustomOptions map[string]string     `json:"custom_options,omitempty"`
	Shipper       addressSnapshot       `json:"shipper"`
	Customer      addressSnapshot       `json:"customer"`
	Parcel        parcelMetrics         `json:"parcel"`
	CustomsInfo   *customsSnapshot      `json:"customs_info,omitempty"`
	Insurance     insuranceSnapshot     `json:"insurance"`
	Origin        canadaPostOrigin      `json:"origin"`
	Destination   canadaPostDestination `json:"destination"`
	InvoiceUUID   string                `json:"invoice_uuid"`
	ClientID      int64                 `json:"client_id"`
	CreatedAt     time.Time             `json:"created_at"`
}

type addressSnapshot struct {
	AddressID    string `json:"address_id"`
	Street1      string `json:"street1"`
	Street2      string `json:"street2"`
	City         string `json:"city"`
	Province     string `json:"province"`
	Zip          string `json:"zip"`
	Phone        string `json:"phone"`
	FullName     string `json:"full_name"`
	Company      string `json:"company"`
	CountryCode  string `json:"country_code"`
	ProvinceCode string `json:"province_code"`
	Country      string `json:"country"`
	Email        string `json:"email"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
}

func snapshotAddress(addr *address.Address) addressSnapshot {
	if addr == nil {
		return addressSnapshot{}
	}
	return addressSnapshot{
		AddressID:    strings.TrimSpace(addr.GetAddressId()),
		Street1:      unwrapStringValue(addr.GetStreet1()),
		Street2:      unwrapStringValue(addr.GetStreet2()),
		City:         unwrapStringValue(addr.GetCity()),
		Province:     unwrapStringValue(addr.GetProvince()),
		Zip:          unwrapStringValue(addr.GetZip()),
		Phone:        unwrapStringValue(addr.GetPhone()),
		FullName:     unwrapStringValue(addr.GetFullName()),
		Company:      unwrapStringValue(addr.GetCompany()),
		CountryCode:  unwrapStringValue(addr.GetCountryCode()),
		ProvinceCode: unwrapStringValue(addr.GetProvinceCode()),
		Country:      unwrapStringValue(addr.GetCountry()),
		Email:        unwrapStringValue(addr.GetEmail()),
		FirstName:    unwrapStringValue(addr.GetFirstName()),
		LastName:     unwrapStringValue(addr.GetLastName()),
	}
}

type customsSnapshot struct {
	EelPfc              string               `json:"eel_pfc"`
	ContentsType        string               `json:"contents_type"`
	ContentsExplanation string               `json:"contents_explanation"`
	RestrictionComments string               `json:"restriction_comments"`
	Currency            string               `json:"currency"`
	CustomItems         []customItemSnapshot `json:"custom_items"`
}

type customItemSnapshot struct {
	Description     string  `json:"description"`
	Quantity        int     `json:"quantity"`
	TotalValueCents int64   `json:"total_value"`
	Weight          float64 `json:"weight"`
	HSTariffNumber  string  `json:"hs_tariff_number"`
	Code            string  `json:"code"`
	OriginCountry   string  `json:"origin_country"`
}

type insuranceSnapshot struct {
	Decimal      string `json:"decimal"`
	CurrencyCode string `json:"currency_code"`
	Amount       int64  `json:"amount"`
}

func snapshotCustoms(info *labels.CustomsInfo) *customsSnapshot {
	if info == nil {
		return nil
	}
	items := make([]customItemSnapshot, 0, len(info.GetCustomItems()))
	currency := ""
	for _, item := range info.GetCustomItems() {
		if item == nil {
			continue
		}
		value := item.GetTotalValue()
		if currency == "" {
			currency = strings.TrimSpace(value.GetCurrencyCode())
		}
		quantity := int(item.GetQuantity())
		if quantity <= 0 {
			quantity = 1
		}
		items = append(items, customItemSnapshot{
			Description:     strings.TrimSpace(item.GetDescription()),
			Quantity:        quantity,
			TotalValueCents: int64(value.GetAmount()),
			Weight:          ouncesToKilograms(float64(item.GetWeight())),
			HSTariffNumber:  strings.TrimSpace(item.GetHsTariffNumber()),
			Code:            strings.TrimSpace(item.GetCode()),
			OriginCountry:   strings.TrimSpace(item.GetOriginCountry()),
		})
	}
	return &customsSnapshot{
		EelPfc:              strings.TrimSpace(info.GetEelPfc()),
		ContentsType:        info.GetContentsType().String(),
		ContentsExplanation: strings.TrimSpace(info.GetContentsExplanation()),
		RestrictionComments: strings.TrimSpace(info.GetRestrictionComments()),
		Currency:            currency,
		CustomItems:         items,
	}
}

func snapshotInsurance(value *money.Money) insuranceSnapshot {
	if value == nil {
		return insuranceSnapshot{}
	}
	return insuranceSnapshot{
		Decimal:      strings.TrimSpace(value.GetDecimal()),
		CurrencyCode: strings.TrimSpace(value.GetCurrencyCode()),
		Amount:       int64(value.GetAmount()),
	}
}

type RateSnapshotStore struct {
	client *redisClient
	ttl    time.Duration
}

func NewRateSnapshotStore(cfg config.RedisConfig) *RateSnapshotStore {
	client := newRedisClient(cfg)
	if client == nil {
		return nil
	}
	ttl := time.Duration(cfg.RateSessionTTLMinutes) * time.Minute
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &RateSnapshotStore{
		client: client,
		ttl:    ttl,
	}
}

func (s *RateSnapshotStore) Save(ctx context.Context, snapshot RateSnapshot) error {
	if s == nil || s.client == nil {
		return errors.New("rate snapshot store not configured")
	}
	if strings.TrimSpace(snapshot.RateID) == "" {
		return errors.New("rate snapshot missing rate_id")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	key := s.key(snapshot.RateID)
	if err := s.client.set(ctx, key, payload, s.ttl); err != nil {
		return err
	}
	return nil
}

func (s *RateSnapshotStore) Load(ctx context.Context, rateID string) (RateSnapshot, error) {
	if s == nil || s.client == nil {
		return RateSnapshot{}, errors.New("rate snapshot store not configured")
	}
	rateID = strings.TrimSpace(rateID)
	if rateID == "" {
		return RateSnapshot{}, errors.New("rate snapshot missing rate_id")
	}
	key := s.key(rateID)
	payload, err := s.client.get(ctx, key)
	if err != nil {
		if errors.Is(err, errRedisNil) {
			return RateSnapshot{}, fmt.Errorf("rate snapshot not found")
		}
		return RateSnapshot{}, err
	}
	var snapshot RateSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return RateSnapshot{}, err
	}
	return snapshot, nil
}

func (s *RateSnapshotStore) key(rateID string) string {
	return fmt.Sprintf("rate:%s", strings.TrimSpace(rateID))
}

func logSnapshotStoreError(rateID string, err error) {
	if err == nil {
		return
	}
	log.Printf("failed to store rate snapshot %s: %v\n", rateID, err)
}
