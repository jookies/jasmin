import { Edit, useForm } from "@refinedev/antd";
import { ConnectorFields } from "./form";

export const ConnectorEdit = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Edit saveButtonProps={saveButtonProps}>
      <ConnectorFields formProps={formProps} editing />
    </Edit>
  );
};
