package authentication

import (
	"context"

	"fmt"

	"github.com/golang-jwt/jwt"
	"github.com/pkg/errors"
	"google.golang.org/grpc/metadata"
)

type CustomClaims struct {
	StoreId uint64      `json:"store_id"`
	Aud     interface{} `json:"aud"`
	jwt.StandardClaims
}

type CustomClaimsWrapper struct {
	CustomClaims
}

var jWTSecret = []byte("027ef85c-8fd2-4462-9a3a-ef780d3af7a6")

func authentication(ctxI context.Context) (ctx context.Context, err error) {
	//	log.Println("YESE HERE")
	var parsedClaims CustomClaimsWrapper

	meta, ok := metadata.FromIncomingContext(ctxI)

	if !ok {

		err = errors.WithStack(errors.New("invalid metadata"))
		return
	}
	//	log.Println(meta.Get("authorization"))
	//	parsedClaims.StoreId = 1
	//	if meta.Len() == 0 {

	//	log.Println("YESE 1")
	if meta.Len() == 0 || len(meta.Get("authorization")) == 0 {
		///log.Println("OMK AAAAADDD")
		err = errors.WithStack(errors.New("authorization metadata is missing"))
		return
	}
	//tokenString := strings.TrimSpace(strings.TrimLeft(meta.Get("authorization")[0], "Bearer"))
	//	tokenString := meta.Get("authorization")[0]

	//	log.Println(tokenString)
	_, err = jwt.ParseWithClaims(meta.Get("authorization")[0], &parsedClaims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("There was an error")
		}

		return jWTSecret, nil
	})
	//	}
	ctx = context.WithValue(ctxI, "store_id", parsedClaims.StoreId)

	return
}

func (c CustomClaims) Valid() (err error) {
	err = c.StandardClaims.Valid()
	if err != nil {
		err = errors.WithStack(err)
		return err
	}

	if c.ExpiresAt == 0 {
		err = errors.WithStack(errors.New("missing exp"))
		return
	}

	if c.IssuedAt == 0 {
		err = errors.WithStack(errors.New("missing iat"))
		return
	}

	if c.StoreId == 0 {
		err = errors.WithStack(errors.New("missing store_id"))
		return
	}

	return
}
