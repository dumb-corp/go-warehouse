package robots

type RobotHTTPHandler struct {
	robotService RobotService
}

type RobotService interface {
}

func NewRobotHTTPHandler(
	robotService RobotService,
) *RobotHTTPHandler {
	return &RobotHTTPHandler{
		robotService: robotService,
	}
}
