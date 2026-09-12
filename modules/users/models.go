package users

// UserRequest is the request body for adding or updating a company member.
// There is no password field: every user authenticates via Google, so
// membership creation never involves setting credentials here.
type UserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
	Role  string `json:"role"`
}

// UserResponse is returned for single-member endpoints.
type UserResponse struct {
	ID    uint   `json:"ID"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
	Role  string `json:"role"`
}

// UserListResponse is returned by the list (company members) endpoint.
type UserListResponse struct {
	Users []UserResponse `json:"users"`
}

// DeleteManyRequest is the body for removing several members at once —
// backs the toolbar's "delete selected" bulk action.
type DeleteManyRequest struct {
	IDs []uint `json:"ids"`
}
