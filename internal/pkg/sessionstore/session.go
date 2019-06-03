package sessionstore

type Session struct {
	Sid      string // session_id
	Uid      string // user_id
	Bid      string // boat_id
	Platform string
}

func (sess *Session) Valid() bool {
	return sess.Sid != "" && sess.Uid != "" && sess.Bid != "" && sess.Platform != ""
}

type SessionSlice []Session

func (p SessionSlice) Len() int           { return len(p) }
func (p SessionSlice) Less(i, j int) bool { return p[i].Sid < p[j].Sid }
func (p SessionSlice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
