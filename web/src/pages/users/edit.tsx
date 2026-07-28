import { Edit, useForm } from "@refinedev/antd";
import { UserFields } from "./form";

export const UserEdit = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Edit saveButtonProps={saveButtonProps}>
      <UserFields formProps={formProps} editing />
    </Edit>
  );
};
