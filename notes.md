# Notes on go
- First write the HTTP Client library for EBS.
- Fields validation issue.
- The good thing in w http.ResponseWriter is that i can, at anytime, just raise 400s! I mean, WOW!
- router handler, such that you'd transfer your request into it.
- multiple response.WriteHeader calls. I totally got that. Now fixing.
- store the incoming request into a EndpointFields handler so you can decode/encode it multiple times. Good 
in case you need to do logging/further validation with the request body.
- parse EBS response body since they don't use proper HTTP codes. You have to parse that off the response body. I will
a slice for that. The good thing is, EBS response is not nested, so I will only search for `responseCode == 0` in the
request body and just either return 200 (if true), or 400 if wrong.
