package consumer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-redis/redis/v7"
	"github.com/noebs/ipin"
	"github.com/sirupsen/logrus"
)

//ResetPassword reset user password after passing some check
func (s *Service) ResetPassword(c *gin.Context) {
	//TODO complete me
	//- we want to make sure that it *was* you
	//-enter your mobile number

}

//NewBiller creates a new biller for system
func (s *Service) NewBiller(c *gin.Context) {
	var b ebs_fields.Merchant

	if err := c.BindJSON(&b); err != nil {
		verr := validationError{Message: "Empty request fields", Code: "empty_fields"}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	p := paymentTokens{redisClient: s.Redis}
	var retry int
begin:
	name := GetRandomName(retry)
	if err := p.newBiller(name); err != nil {
		retry++
		goto begin
	}
	c.JSON(http.StatusCreated, gin.H{"result": "ok", "namespace": name})

}

//CardFromNumber gets the gussesed associated mobile number to this pan
func (s *Service) CardFromNumber(c *gin.Context) {
	// the user must submit in their mobile number *ONLY*, and it is get
	q, ok := c.GetQuery("mobile_number")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"message": "mobile number is empty", "code": "empty_mobile_number"})
		return
	}
	// now search through redis for this mobile number!
	// first check if we have already collected that number before
	pan, err := s.Redis.Get(q + ":pan").Result()
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"result": pan})
		return
	}
	username, err := s.Redis.Get(q).Result()
	if err == redis.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
		return
	}
	if pan, ok := utils.PanfromMobile(username, s.Redis); ok {
		c.JSON(http.StatusOK, gin.H{"result": pan})
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
	}

}

//GetCards Get all cards for the currently authorized user
func (s *Service) GetCards(c *gin.Context) {
	username := c.GetString("username")
	if username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		return
	}
	cards, err := s.Redis.ZRange(username+":cards", 0, -1).Result()
	if err != nil {
		// handle the error somehow
		logrus.WithFields(logrus.Fields{
			"error":   "unable to get results from redis",
			"message": err.Error(),
		}).Info("unable to get results from redis")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "error in redis"})
		return
	}
	cardBytes := cardsFromZ(cards)
	m, _ := s.Redis.HGet(username+":cards", "main_card").Result()
	mCard := cardsFromZ([]string{m})
	c.JSON(http.StatusOK, gin.H{"cards": cardBytes, "main_card": mCard[0]})

}

//AddCards Allow users to add card to their profile
// if main_card was set to true, then it will be their main card AND
// it will remove the previously selected one FIXME
func (s *Service) AddCards(c *gin.Context) {
	var fields ebs_fields.CardsRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	//check if the card is not from non EBS affiliated banks
	//TODO make sure that the card is not from private switch
	// checkCardIsWorking(fields.PAN)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
		return
	}
	// check isEbs
	if notEbs(fields.PAN) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Card not supported (not compatible with EBS)", "code": "card_not_supported"})
		return
	}
	buf, _ := json.Marshal(fields)
	username := c.GetString("username")
	if username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		return
	}
	// make sure the length of the card and expDate is valid
	z := &redis.Z{
		Member: buf,
	}
	if fields.IsMain {
		// refactor me, please!
		ucard := card{"main_card": buf, "pan": fields.PAN, "exp_date": fields.Expdate}
		s.Redis.HMSet(username, ucard)
		s.Redis.ZAdd(username+":cards", z)
		return
	}
	_, err = s.Redis.ZAdd(username+":cards", z).Result()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	// also it is necessary to add it into a list of user's pans
	//FIXME better handle this error smh
	s.Redis.RPush(username+":pans", fields.PAN)

	c.JSON(http.StatusCreated, gin.H{"username": username, "cards": fields})

}

//EditCard allow authorized users to edit their cards (e.g., edit pan / expdate)
func (s *Service) EditCard(c *gin.Context) {
	var card ebs_fields.CardsRedis

	err := c.ShouldBindWith(&card, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "unmarshalling_error"})
		return
	}
	username := c.GetString("username")
	if username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		return
	} else if card.ID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "card id not provided", "code": "card_id_not_provided"})
		return
	}

	// rm card
	if card.IsMain {
		s.Redis.HDel(username+":cards", "main_card")
	} else {
		s.Redis.ZRemRangeByRank(username+":cards", int64(card.ID-1), int64(card.ID-1))
	}
	//card.RmCard(username, card.ID)
	buf, err := json.Marshal(card)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	z := &redis.Z{
		Member: buf,
	}
	s.Redis.ZAdd(username+":cards", z)
	if card.IsMain {
		s.Redis.HSet(username, "main_card", buf)
	}

	c.JSON(http.StatusOK, gin.H{"username": username, "cards": card})
}

//RemoveCard allow authorized users to remove their card
// when the send the card id (from its list in app view)
func (s *Service) RemoveCard(c *gin.Context) {

	var fields ebs_fields.ItemID
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
		// there is no error in the request body
	} else {
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else if fields.ID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "card id not provided", "code": "card_id_not_provided"})
			return
		}
		// core functionality
		id := fields.ID

		if fields.IsMain {
			s.Redis.HDel(username+":cards", "main_card")
		} else {
			_, err := s.Redis.ZRemRangeByRank(username+":cards", int64(id-1), int64(id-1)).Result()
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "unable_to_delete"})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"username": username, "cards": fields})
	}

}

//AddMobile adds a mobile number to the current authorized user
func (s *Service) AddMobile(c *gin.Context) {

	var fields ebs_fields.MobileRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else {
			if fields.IsMain {
				s.Redis.HSet(username, "main_mobile", buf)
				s.Redis.SAdd(username+":cards", buf)
			} else {
				s.Redis.SAdd(username+":mobile_numbers", buf)
			}

			c.JSON(http.StatusCreated, gin.H{"username": username, "cards": string(buf)})
		}
	}

}

//GetMobile gets list of mobile numbers to this user
func (s *Service) GetMobile(c *gin.Context) {

	var fields ebs_fields.CardsRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else {
			if fields.IsMain {
				s.Redis.HSet(username, "main_mobile", buf)
				s.Redis.SAdd(username+":mobile_numbers", buf)
			} else {
				s.Redis.SAdd(username+":mobile_numbers", buf)
			}

			c.JSON(http.StatusCreated, gin.H{"username": username, "mobile_numbers": string(buf)})
		}
	}

}

//NecToName gets an nec number from the context and maps it to its meter number
func (s *Service) NecToName(c *gin.Context) {
	if nec := c.Query("nec"); nec != "" {
		name, err := s.Redis.HGet("meters", nec).Result()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "No user found with this NEC", "code": "nec_not_found"})
		} else {
			c.JSON(http.StatusOK, gin.H{"result": name})
		}
	}
}

func (s *Service) cacheKeys(c *gin.Context) {
	// it should check ebs first

}

var billerChan = make(chan billerForm)

//CancelBiller using its issued uuid
func (s *Service) CancelBiller(c *gin.Context) {

	if v, ok := c.GetQuery("id"); !ok || v == "" {
		vErr := validationError{Code: "missing_uuid", Message: "UUID not presented"}
		c.JSON(http.StatusBadRequest, vErr)
		return
	} else {
		p := paymentTokens{redisClient: s.Redis}
		if err := p.cancelTransaction(v); err != nil {
			vErr := validationError{Code: "internal_error", Message: err.Error()}
			c.JSON(http.StatusBadRequest, vErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": true})
	}

}

//CancelBiller using its issued uuid
func (s *Service) info(c *gin.Context) {

	b, ok := c.GetQuery("biller")
	if !ok || b == "" {
		vErr := validationError{Code: "missing_biller", Message: "Biller ID not presented"}
		c.JSON(http.StatusBadRequest, vErr)
		return
	}

	id, _ := c.GetQuery("id")
	// if !ok || id == "" {
	// 	vErr := validationError{Code: "missing_uuid", Message: "UUID not presented"}
	// 	c.JSON(http.StatusBadRequest, vErr)
	// 	return
	// }
	clientID, _ := c.GetQuery("refID")

	p := paymentTokens{redisClient: s.Redis}

	// how get by id works
	if res, err := p.getByID(b, id, clientID); err != nil {
		// vErr := validationError{Code: "not_found", Message: err.Error()}
		c.JSON(http.StatusBadRequest, billerForm{ID: clientID, IsSuccessful: false, EBS: ebs_fields.GenericEBSResponseFields{ResponseMessage: "not_completed", ResponseCode: -1}})
		return
	} else {
		c.JSON(http.StatusOK, res)
	}

}

//BillerTrans to get all transaction to specific biller_id
func (s *Service) BillerTrans(c *gin.Context) {

	b, ok := c.GetQuery("biller")
	if !ok || b == "" {
		vErr := validationError{Code: "missing_biller", Message: "Biller ID not presented"}
		c.JSON(http.StatusBadRequest, vErr)
		return
	}

	p := paymentTokens{redisClient: s.Redis}

	// how get by id works
	if res, err := p.getTrans(b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"result": res})
		return
	} else {
		c.JSON(http.StatusOK, gin.H{"result": res})
	}

}

//BillerHooks submits results to external endpoint
func BillerHooks() {

	for {
		select {
		case value := <-billerChan:
			log.Printf("The recv is: %v", value)
			data, _ := json.Marshal(&value)
			// FIXME this code is dangerous
			if value.to == "" {
				value.to = "http://test.tawasuloman.com:8088/ShihabSudanWS/ShihabEBSConfirmation"
			}
			if _, err := http.Post(value.to, "application/json", bytes.NewBuffer(data)); err != nil {
				log.Printf("the error is: %v", err)
			}
		}
	}
}

//cashOutClaim used
func (p *paymentTokens) cashOutClaims(ns, id, toCard string) error {

	token, _ := uuid.NewRandom()
	log.Printf("the ns is: %v - id is: %v", ns, id)
	card, err := p.GetCashOut(ns)
	log.Printf("The card info is: %#v", card)

	if err != nil {
		log.Printf("error in cashout: %v", err)
		return err
	}
	ipinBlock, err := ipin.Encrypt("MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBANx4gKYSMv3CrWWsxdPfxDxFvl+Is/0kc1dvMI1yNWDXI3AgdI4127KMUOv7gmwZ6SnRsHX/KAM0IPRe0+Sa0vMCAwEAAQ==", card.Ipin, token.String())
	if err != nil {
		log.Printf("error in encryption: %v", err)
		return err
	}

	amount, err := p.getAmount(ns, id)
	if err != nil {
		log.Printf("error in cashout amount: %v", err)
		return err
	}

	data := ebs_fields.ConsumerCardTransferFields{
		ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
			ApplicationId: "ACTSCon",
			TranDateTime:  "022821135300",
			UUID:          token.String(), // this is *WRONG*
		},
		ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{
			Pan:     card.Pan,
			Ipin:    ipinBlock,
			ExpDate: card.ExpDate,
		},
		AmountFields: ebs_fields.AmountFields{
			TranAmount:       float32(amount), // it should be populated
			TranCurrencyCode: "SDG",
		},
		ToCard: toCard,
	}

	log.Printf("the request is: %v", data)
	req, _ := json.Marshal(&data)

	res, err := http.Post("http://localhost:8080/consumer/p2p", "application/json", bytes.NewBuffer(req))
	if err != nil {
		log.Printf("Error in response: %v", err)
		return err
	}

	resData, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Printf("Error in reading noebs response: %v", err)
		return err
	}

	log.Printf("the response is: %v", string(resData))

	parser, err := newFromBytes(resData, res.StatusCode)
	if err != nil {
		log.Printf("Error in reading noebs response: %v", err)
		return err
	}

	// now we gotta marshal the response smh
	defer res.Body.Close()
	cardRes := cashoutFields{Biller: parser, Amount: card.Amount, Name: card.Name}

	msg, err := json.Marshal(&cardRes)
	if err != nil {
		log.Printf("Error in parsing marshal: %v", err)
		return err
	}

	// now use pub sub

	{
		pubsub := p.redisClient.Subscribe("chan_cashouts")

		// Wait for confirmation that subscription is created before publishing anything.
		_, err := pubsub.Receive()
		if err != nil {
			log.Printf("Error in pubsub: %v", err)
			return err
		}

		// Publish a message.
		err = p.redisClient.Publish("chan_cashouts", msg).Err() // So, we are currently just pushing to the data
		if err != nil {
			log.Printf("Error in pubsub: %v", err)
			return err
		}

		time.AfterFunc(time.Second, func() {
			// When pubsub is closed channel is closed too.
			_ = pubsub.Close()
		})
	}

	return nil
}

//NewCashout experimental interface to make cashout publicaly availabe.
func NewCashout(r *redis.Client) paymentTokens {
	return paymentTokens{redisClient: r}
}

//pub experimental support to add pubsub support
// we need to make this api public
func (p *paymentTokens) CashoutPub() {
	pubsub := p.redisClient.Subscribe("chan_cashouts")

	// Wait for confirmation that subscription is created before publishing anything.
	_, err := pubsub.Receive()
	if err != nil {
		log.Printf("There is an error in connecting to chan.")
		return
	}

	// // Go channel which receives messages.
	ch := pubsub.Channel()

	// Consume messages.
	var card cashoutFields
	for msg := range ch {
		// So this is how we gonna do it! So great!
		// we have to parse the payload here:
		if err := json.Unmarshal([]byte(msg.Payload), &card); err != nil {
			log.Printf("Error in marshaling data: %v", err)
			continue
		}

		data, err := json.Marshal(&card)
		if err != nil {
			log.Printf("Error in marshaling response: %v", err)
			continue
		}
		_, err = http.Post(card.Endpoint, "application/json", bytes.NewBuffer(data))
		if err != nil {
			log.Printf("Error in response: %v", err)
		}
		fmt.Println(msg.Channel, msg.Payload)
	}
}

func (p *paymentTokens) pubSub(channel string, message interface{}) {
	pubsub := p.redisClient.Subscribe(channel)

	// Wait for confirmation that subscription is created before publishing anything.
	_, err := pubsub.Receive()
	if err != nil {
		panic(err)
	}

	// // Go channel which receives messages.
	ch := pubsub.Channel()

	time.AfterFunc(time.Second, func() {
		// When pubsub is closed channel is closed too.
		_ = pubsub.Close()
	})

	// Consume messages.
	for msg := range ch {
		fmt.Println(msg.Channel, msg.Payload)
	}
}
